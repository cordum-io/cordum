# Releasing Cordum

This checklist governs tagging and publishing a Cordum release. The
publish pipeline (`.github/workflows/docker.yml`) runs on tag push and
produces multi-arch signed container images, SBOMs, provenance, and an
OCI-packaged Helm chart. These steps exist to catch breakage **before**
the tag hits the world.

Anything in this document is authoritative — if something here
contradicts a wiki page or a Slack thread, this file wins. Update it
when the release process changes.

## Pre-release checklist

Run through this list in order. Do **not** skip ahead — a later step
often depends on the earlier one having succeeded.

### 1. Dry-run the publish workflow

Before tagging, trigger the publish workflow against `main` to confirm
the build graph is green:

```bash
gh workflow run docker.yml --ref main
gh run watch --exit-status
```

If `docker.yml` does not yet expose a `workflow_dispatch` trigger, push
a throwaway pre-release tag instead (`v0.0.0-dryrun.N`) — the stable-tag
guard means this will publish version-pinned images without moving
`:latest`. Delete the tag afterwards.

### 2. Confirm every image published

After `git push origin v<VERSION>`, wait for all jobs in `docker.yml`
to complete, then check that every package exists at the new tag:

```bash
TAG=1.2.3   # strip leading v
for pkg in api-gateway scheduler safety-kernel workflow-engine context-engine mcp dashboard; do
  docker manifest inspect "ghcr.io/cordum-io/cordum/${pkg}:${TAG}" >/dev/null \
    && echo "OK   ${pkg}:${TAG}" \
    || echo "MISS ${pkg}:${TAG}"
done
```

All 7 packages must print `OK`. Investigate any `MISS` immediately —
the release is incomplete.

If this is the **first** time a package is published (e.g. the initial
release that ships `cordum/mcp`), also walk the public-visibility
checklist at
[docs/deployment/ghcr-public-access.md](docs/deployment/ghcr-public-access.md).

### 3. Smoke test the compose stack

On a clean Docker Desktop install (or `docker system prune -a --volumes`
first), run the exact sequence a new user will execute:

```bash
git clone https://github.com/cordum-io/cordum.git /tmp/cordum-release-smoke
cd /tmp/cordum-release-smoke
export CORDUM_API_KEY=$(openssl rand -hex 32)
export REDIS_PASSWORD=$(openssl rand -hex 16)
export CORDUM_VERSION=${TAG}
docker compose -f docker-compose.yml pull
docker compose -f docker-compose.yml up -d
docker compose -f docker-compose.yml ps
```

Every service must reach `healthy` (or `running` for images with no
healthcheck) within 120 s. Record the `docker compose ps` output in
the release notes.

Also smoke the source-install path from a clean clone:

```bash
cd /tmp/cordum-release-smoke
./tools/scripts/quickstart.sh --clean --health-timeout 300
```

The script must finish with the "Cordum is running!" banner and the
platform smoke test must pass (approval workflow reaches `succeeded`).

### 4. Verify cosign signatures

Pick any two images at random and verify:

```bash
for pkg in api-gateway dashboard; do
  cosign verify "ghcr.io/cordum-io/cordum/${pkg}:${TAG}" \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com \
    --certificate-identity-regexp 'https://github\.com/cordum-io/cordum/\.github/workflows/docker\.yml@refs/tags/v.*' \
    > /dev/null \
    && echo "OK   ${pkg}" \
    || echo "BAD  ${pkg}"
done
```

Any `BAD` means the signature is missing or was produced by the wrong
workflow identity — block the release and investigate.

Full cosign recipe (including SBOM extraction and Helm chart
verification) lives in
[docs/deployment/images.md](docs/deployment/images.md).

### 5. Tear down smoke artefacts

```bash
docker compose -f /tmp/cordum-release-smoke/docker-compose.yml down -v
rm -rf /tmp/cordum-release-smoke
```

## Copy-paste verification script

Drop the block below into a clean shell to run all checks against the
tag in `${TAG}`:

```bash
#!/usr/bin/env bash
set -euo pipefail

: "${TAG:?export TAG=<version-without-v> first}"

packages=(api-gateway scheduler safety-kernel workflow-engine context-engine mcp dashboard)

echo "--- manifest presence"
for pkg in "${packages[@]}"; do
  if docker manifest inspect "ghcr.io/cordum-io/cordum/${pkg}:${TAG}" >/dev/null 2>&1; then
    echo "OK   manifest ${pkg}:${TAG}"
  else
    echo "MISS manifest ${pkg}:${TAG}"
  fi
done

echo "--- cosign signatures"
for pkg in "${packages[@]}"; do
  if cosign verify "ghcr.io/cordum-io/cordum/${pkg}:${TAG}" \
       --certificate-oidc-issuer https://token.actions.githubusercontent.com \
       --certificate-identity-regexp 'https://github\.com/cordum-io/cordum/\.github/workflows/docker\.yml@refs/tags/v.*' \
       >/dev/null 2>&1; then
    echo "OK   signed    ${pkg}:${TAG}"
  else
    echo "BAD  signed    ${pkg}:${TAG}"
  fi
done

echo "--- multi-arch coverage"
for pkg in "${packages[@]}"; do
  arches=$(docker manifest inspect "ghcr.io/cordum-io/cordum/${pkg}:${TAG}" \
    | jq -r '.manifests[].platform.architecture' | sort -u | paste -sd, -)
  echo "arches ${pkg}: ${arches}"
done
```

Every line must be `OK ...` or `arches ...: amd64,arm64` (dashboard may
be amd64-only during the transition period — see Issue #TBD).

## Post-release

1. Create the GitHub Release via `gh release create v${TAG}`, attaching
   the changelog and any platform binaries uploaded by the publish
   workflow.
2. Announce in `#general` with a link to the release page.
3. Open an issue tagged `post-release` to track any deferred follow-ups.

## Documentation sync

Cordum docs live on **four surfaces**. Any quickstart or user-facing
doc edit has to land on all four before it ships — otherwise users on
cordum.io read a different story than users of the repo.

The four surfaces:

| # | Surface | Path | Audience |
|---|---------|------|----------|
| 1 | **Source** | `cordum/docs/*.md` | Repo readers via `git clone`; the canonical copy. |
| 2 | **Docusaurus (core)** | `cordum/docs-site/docs/**` | GitHub Pages build at `docs.cordum.io`. Mostly identical to #1 with Docusaurus frontmatter. |
| 3 | **Docusaurus (marketing)** | `Cordum-site/docs-site/docs/**` | `cordum.io/docs` (marketing-site docs tab). Body should mirror #1 verbatim modulo frontmatter. |
| 4 | **Next.js marketing pages** | `Cordum-site/site/src/app/docs/<slug>/page.tsx` + `docsData.ts` | `cordum.io/docs/<slug>` hand-rolled pages with "Autonomous AI Agents / Agent Control Plane" framing per `Cordum-site/CLAUDE.md`. |

### Required edit order

1. **Edit the source** at `cordum/docs/<page>.md`. This is the only
   copy you *author*. Every downstream surface either mirrors or
   paraphrases from here.
2. **Rebuild the core Docusaurus site** to catch Markdown → Docusaurus
   issues locally:
   ```bash
   cd cordum/docs-site && npm run build
   ```
   Fix any broken-link warnings before moving on.
3. **Mirror to the marketing Docusaurus site.** Copy the source body
   verbatim into `Cordum-site/docs-site/docs/<path>/<page>.md`,
   preserving only the marketing-site frontmatter (`title`,
   `sidebar_position`, `slug`) and any docusaurus-specific enrichments
   (`:::tip` blocks, cross-refs to marketing-only tutorials) that have
   no equivalent upstream.
4. **Update the Next.js hand-rolled page** at
   `Cordum-site/site/src/app/docs/<slug>/page.tsx` if this page has a
   bespoke marketing rewrite. Update `Cordum-site/site/src/app/docs/docsData.ts`
   if the nav title or description changed. Keep the marketing voice
   ("Autonomous AI Agents", "Agent Control Plane") — refer to
   `Cordum-site/CLAUDE.md` for the brand guardrails.
5. **Validate the marketing site:**
   ```bash
   cd Cordum-site/site
   npm run lint
   NEXT_DIST_DIR=.next-verify npm run build
   ```
   The `NEXT_DIST_DIR` override is per `Cordum-site/CLAUDE.md` —
   required on Windows when `.next` is locked by a dev server.
6. **Confirm lychee passes** either locally (`lychee ./docs/**/*.md
   ./docs-site/docs/**/*.md`) or by pushing to a PR branch where the
   `Deploy Docs to GitHub Pages / link-check` CI job runs on every
   docs edit.

### Why the four-surface structure exists

- The **source** is the git-cloneable README for engineers who live
  in the repo. Narrow, precise, code-adjacent voice.
- The **core Docusaurus site** deploys from the same repo via GitHub
  Pages and is the "neutral" documentation portal.
- The **marketing Docusaurus site** is served under `cordum.io` and
  exists in the Cordum-site repo so marketing can iterate on chrome,
  nav, and branding without touching the core repo.
- The **Next.js pages** are curated landing pages for the docs that
  matter most to new users. They carry the category-positioning copy
  ("Agent Control Plane for Autonomous AI Agents") that doesn't fit
  inside raw Markdown.

Future editors: if you change any of surfaces 1–3 without also
refreshing 4, cordum.io ends up telling a different story than the
repo. Past mistakes on this front (cordumctl quickstart referenced
in the marketing hero after the command was deleted from the repo)
are the reason this section exists.
