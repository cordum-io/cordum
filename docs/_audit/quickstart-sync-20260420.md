# Quickstart Docs Sync Audit — 2026-04-20

Scope: verify the three downstream surfaces against the canonical
`cordum/docs/quickstart.md` and flag every drift that needs to land
before this task can close. The canonical source is NOT in scope for
this task — it was already rewritten during the `task-df5343a0` revert
and is treated as the source of truth.

## Surfaces checked

| # | Path | Role |
|---|------|------|
| 0 | `cordum/docs/quickstart.md` | Canonical — authoritative. Do not edit. |
| 1 | `Cordum-site/docs-site/docs/getting-started/quickstart.md` | Marketing Docusaurus mirror (`cordum.io/docs`). |
| 2 | `Cordum-site/site/src/app/docs/quickstart/page.tsx` + `docsData.ts` | Marketing Next.js page (hand-rolled, "Agent Control Plane" framing). |
| 3 | `cordum/docs/quickstart-cold-test.md` | Internal human tester checklist. |

## Canonical snapshot (baseline)

- Primary command: `./tools/scripts/quickstart.sh` (run after
  `git clone` + `cd cordum`).
- Framing: "One command, ~3 minutes on first run, ~30 seconds afterwards."
- Prereqs: Docker Desktop v4+ / Docker Engine 20.10+ with Compose v2,
  4 GB RAM, Go 1.24+ (cert gen only), curl, openssl, jq optional.
- Outputs table, platform notes (Windows/macOS/Linux), useful flags
  (`--clean`, `--skip-build`, `--skip-smoke`, `--health-timeout`,
  `--artifacts-dir`), env overrides table, teardown, troubleshooting
  table, manual walkthrough pointer (→ `cordumctl.md`,
  `workflow-step-types.md`), next steps.
- Zero references to a `cordumctl quickstart` subcommand (removed
  during revert).

## Drift findings per surface

### Surface 1 — `Cordum-site/docs-site/docs/getting-started/quickstart.md`

- **Status:** In sync with the pattern QA approved on the prior sibling
  task (mem-470ca6b3 / mem-028416b6).
- **Shape:** Docusaurus frontmatter (`title`, `sidebar_position`,
  `slug`), a `:::tip` block pointing readers at the marketing-only
  Guardrails Demo, a "Fastest path" block calling
  `./tools/scripts/quickstart.sh`, an 8-step manual walkthrough
  (export API key → compose up → status → workflow CRUD → approve
  gate → poll status → cleanup), troubleshooting table, next steps.
- **Drift vs canonical (intentional, QA-approved):** The marketing
  Docusaurus mirror keeps the manual API walkthrough inline instead
  of delegating to `cordumctl.md` / `workflow-step-types.md`. This is
  a deliberate usability choice — readers on `cordum.io/docs` don't
  necessarily want to cross-chase repo docs. The fast-path command
  and prereqs are byte-identical to the canonical, and there are zero
  `cordumctl quickstart` references.
- **Drift vs canonical (unintentional):** none found.
- **Action:** no rewrite. Step 2 is a no-op verification — rebuild
  Docusaurus locally to confirm zero new broken-link warnings
  (pre-existing unrelated warnings are tolerated per mem-4ce59534).

### Surface 2 — `Cordum-site/site/src/app/docs/quickstart/page.tsx`

- **Status:** In sync with the canonical command set and the
  marketing-site CLAUDE.md brand guardrails.
- **Shape:** Metadata with "Agent Control Plane for Autonomous AI
  Agents" framing, JSON-LD blocks (`techArticle`, `howTo`,
  `breadcrumb`), prereq pills, "Fastest path" `CodeWindow` with
  `./tools/scripts/quickstart.sh`, "Alternative bring-up" block with
  `go run ./cmd/cordumctl up` and raw `docker compose up -d`,
  dashboard URL callout, licensing + telemetry cards, verify block
  (curl + `platform_smoke.sh`), troubleshooting cards, "Source of
  truth" link back to `docs/quickstart.md`.
- **Drift vs canonical:** none. The page calls out the same
  canonical command, mentions `cordumctl up` and `docker compose`
  as supported alternatives (both valid per the revert), and never
  mentions the removed `cordumctl quickstart` subcommand.
- **`docsData.ts`:** The nav entry is `{ label: "Quickstart",
  href: "/docs/quickstart" }`. There is no description field to sync
  — nav data has only `label`/`href`. No stale command strings.
- **Action:** no rewrite. Step 3 is a no-op verification — run
  `npm run lint` + `NEXT_DIST_DIR=.next-verify npm run build` to
  confirm the marketing site still compiles.

### Surface 3 — `cordum/docs/quickstart-cold-test.md`

- **Status:** In place.
- **Shape:** HUMAN COLD-TEST ONLY banner at top, tester-background
  gates, 10-row checklist table (elapsed / exit / observed / notes),
  sign-off form (tester, date, host OS, Docker version, total elapsed,
  pass/fail), free-text feedback block, recommended-edits table, and
  submission instructions ("commit filled copy under
  `docs/cold-tests/YYYY-MM-DD-tester-os.md`").
- **Drift vs plan (step 4):** none. The template banner explicitly
  cites the `task-75273093` 6-reject precedent.
- **Action:** no rewrite. Step 4 is a no-op verification.

## Non-mirror drift noted for follow-up (OUT OF SCOPE for this task)

- `cordum/docs-site/docs/getting-started/quickstart.md` (the **core**
  Docusaurus site, surface #2 in `RELEASING.md §"Documentation sync"`)
  currently reads: `make dev-up` + `curl http://localhost:8081/health`.
  That is the **old flow** — it predates the revert and does not
  reference `./tools/scripts/quickstart.sh`. This task is scoped to
  the marketing mirrors (Cordum-site) per the reopenReason, so
  syncing the core Docusaurus mirror is deferred to a follow-up task.
  Flag for QA: reopen fold-in is likely a 10-line rewrite.

- `cordum/.github/workflows/docs-deploy.yml` currently has NO
  `link-check` job wired — only `build` and `deploy` jobs for the
  core Docusaurus site. `RELEASING.md §6` already references "the
  `Deploy Docs to GitHub Pages / link-check` CI job" as the expected
  name, so the CI job name is already spoken for. **This is the
  primary residual code change for step 5.**

- `cordum/.lycheeignore` **exists** with the expected patterns
  (localhost/127.0.0.1/0.0.0.0, mailto/tel, example.com,
  placeholder.invalid, ghcr.io, redis/nats schemes). No change needed.

- `cordum/RELEASING.md §"Documentation sync"` exists and documents
  the four-surface sync order (source → core Docusaurus → marketing
  Docusaurus → marketing Next.js). No change needed for step 6 first
  half; step 6 second half is the human-tester chat handoff.

## Zero stale `cordumctl quickstart` references

`grep -rn "cordumctl quickstart"` across `cordum/docs/`,
`Cordum-site/docs-site/docs/`, `Cordum-site/site/src/` → 0 matches.

## Residual checklist for downstream steps

- **Step 2 (docs-site mirror):** verify-only; rebuild to confirm no
  new broken-link warnings.
- **Step 3 (marketing Next.js page):** verify-only; lint + build.
- **Step 4 (cold-test template):** verify-only; file is in place.
- **Step 5 (lychee CI):** add `link-check` job to
  `.github/workflows/docs-deploy.yml` pinned to a lychee-action
  release tag (not `@latest`), path-scoped to `docs/**` and
  `docs-site/docs/**`, runtime budget < 60s.
- **Step 6 (docs-sync runbook + human handoff):** RELEASING.md
  section is present. Execute the `#general` chat handoff mentioning
  `@human` with a link to the cold-test template. Do NOT move the
  task to REVIEW until a human tester signs the artefact — this is
  the rail `task-75273093` was reopened 6× for self-certifying.

## Sign-off

Audit produced by `worker-aa42` on `2026-04-20`.
Source commits inspected: current working tree (no PR branch).
