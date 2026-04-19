# GHCR public-access checklist (one-time per package)

When a Cordum service image is published to `ghcr.io/cordum-io/cordum/<name>`
for the **first time**, the package starts out with **private** visibility.
Anonymous `docker pull` fails with `denied: not authorized` until a repo
admin flips visibility to public. This is a one-time-per-package
operation; subsequent pushes inherit the public setting.

This document is the checklist a release manager runs after the very
first push of any new image (`mcp` and any future service) to
`ghcr.io/cordum-io/cordum`.

## Prerequisites

- Repo admin access on `cordum-io/cordum`.
- `gh` CLI installed and authenticated.
- A GitHub PAT with `admin:packages` scope (a fine-grained token works
  if it has "Manage packages" read+write for the org). Set it in the
  shell that will run the `gh api` commands:
  ```bash
  export GH_TOKEN=ghp_xxx
  ```

## Option A — UI (one package at a time)

1. Open https://github.com/orgs/cordum-io/packages
2. Click the package name (e.g. `cordum/api-gateway`).
3. Scroll to **Danger Zone** → **Change visibility**.
4. Select **Public** and confirm.
5. Anonymous pull works immediately:
   ```bash
   docker pull ghcr.io/cordum-io/cordum/api-gateway:latest
   ```

## Option B — Scripted (all packages in one shot)

```bash
#!/usr/bin/env bash
# Run with GH_TOKEN set to a PAT that has admin:packages.
set -euo pipefail

ORG=cordum-io
PACKAGES=(
  api-gateway
  scheduler
  safety-kernel
  workflow-engine
  context-engine
  mcp
  cordumctl
  dashboard
)

for pkg in "${PACKAGES[@]}"; do
  encoded="cordum%2F${pkg}"
  echo "Setting ${pkg} visibility=public ..."
  gh api -X PATCH \
    "/orgs/${ORG}/packages/container/${encoded}/visibility" \
    -f visibility=public
done
```

The path uses `/orgs/<org>/packages/container/<encoded-name>/visibility`
with the package name URL-encoded (`%2F` for the `/` separator). The
[GitHub Packages REST API reference] confirms the endpoint shape and the
`admin:packages` scope requirement.

[GitHub Packages REST API reference]: https://docs.github.com/en/rest/packages/packages

## Per-package checklist

Track each new image's visibility flip here:

- [ ] `cordum/api-gateway`
- [ ] `cordum/scheduler`
- [ ] `cordum/safety-kernel`
- [ ] `cordum/workflow-engine`
- [ ] `cordum/context-engine`
- [ ] `cordum/mcp`
- [ ] `cordum/cordumctl`
- [ ] `cordum/dashboard`

Tick off each box once anonymous `docker pull ghcr.io/cordum-io/cordum/<pkg>`
succeeds from a workstation with no `docker login`.

## Verifying

```bash
# From a clean shell — no `docker login` first.
for pkg in api-gateway scheduler safety-kernel workflow-engine context-engine mcp cordumctl dashboard; do
  if docker pull --quiet "ghcr.io/cordum-io/cordum/${pkg}:latest" >/dev/null; then
    echo "OK   ${pkg}"
  else
    echo "FAIL ${pkg}"
  fi
done
```

Any `FAIL` row indicates the visibility flip is still pending for that
package.
