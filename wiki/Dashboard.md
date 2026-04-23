# Dashboard

The Cordum dashboard is a React UI for workflows, jobs, packs, and policies.

## Run locally (dev)

```bash
cd dashboard
npm install
npm run dev
```

Update `dashboard/public/config.json` to point to your gateway:

```json
{
  "apiBaseUrl": "http://localhost:8081",
  "apiKey": "",
  "tenantId": "default",
  "principalId": "dashboard",
  "principalRole": "admin"
}
```

## Run in Docker

```bash
docker build -t cordum-dashboard -f dashboard/Dockerfile dashboard

docker run --rm -p 8082:8080 \
  -e CORDUM_API_BASE_URL=http://localhost:8081 \
  -e CORDUM_TENANT_ID=default \
  cordum-dashboard
```

## Runtime configuration

The container writes `config.json` at startup from environment variables:

- `CORDUM_API_BASE_URL` (empty = same origin)
- `CORDUM_API_KEY` (embedded only when `CORDUM_DASHBOARD_EMBED_API_KEY=1`)
- `CORDUM_TENANT_ID`
- `CORDUM_PRINCIPAL_ID`
- `CORDUM_PRINCIPAL_ROLE`

For security, the dashboard does not persist API keys in localStorage. Keys live
in memory unless you explicitly embed them in `config.json`.

The default Compose stack embeds the API key into the dashboard config for local
development (`CORDUM_DASHBOARD_EMBED_API_KEY=true`). Remove that variable in
shared environments to require manual auth.

## AppShell visual parity (task-2c60fa67)

Shell parity criteria and deviations are tracked in `dashboard/DESIGN_PARITY_CHECKLIST.md`.

Highlights:
- Desktop shell uses fixed `240px` sidebar, compact utility controls, and tokenized shell surfaces (`shell-sidebar`, `shell-header`, `shell-panel`).
- Navigation/toggle semantics include `aria-expanded` + `aria-controls`; icon-only controls have explicit labels.
- Supporting indicators (connection/environment/maintenance) use compact semantic status styling and preserve live-state messaging.

Validation record on **February 24, 2026** (`dashboard/`):
- ✅ `npm run typecheck`
- ✅ `npm run test -- src/components/layout/AppShell.test.tsx src/components/ConnectionIndicator.test.ts`
- ✅ `npm run test -- src/pages/settings/InputSafetySettings.test.tsx`
- ✅ `npm test`
- ✅ `npm run build`

## Security Overview parity (task-eaf5f368)

Security Overview parity criteria and gate records are tracked in `dashboard/DESIGN_PARITY_CHECKLIST.md`.

Highlights:
- KPI cards use compact instrument-card anatomy with semantic accents and explicit fallback values.
- Needs Attention + Live Safety feed rows were tuned for dense scan rhythm with improved keyboard/focus semantics.
- Security Attention Breakdown chart uses tokenized chart theming (`chart-theme`) and reduced-motion-safe animation defaults (`chartMotionProps`).
- Query-backed security panels now expose explicit loading/empty/error/success states with retry affordances.

Validation record on **February 24, 2026** (`dashboard/`):
- ✅ `npm run typecheck`
- ✅ `npm test`
- ✅ `npm run build`

## Operations pages parity (task-a013821c)

Operations parity criteria and accepted deviations are tracked in `dashboard/DESIGN_PARITY_CHECKLIST.md`.

Highlights:
- **Approvals:** explicit loading/empty/error/success queue states, stronger filter/action accessibility semantics, and stricter dialog semantics for review workflows.
- **Jobs:** compact table/control-surface rhythm with mono ID/time voice, compact semantic status/decision chips, and consistent pagination affordances.
- **Runs:** compact KPI/filter/list/detail hierarchy with semantic governance outcomes while preserving stream integration and drill-down routes.
- **Cross-page language:** shared compact status semantics via `StatusBadge`, `Badge` density support, and normalized status token formatting in `src/lib/status.ts` and `src/lib/format.ts`.
- **Non-regression:** role-gated actions, URL/query behavior, and workflow mutations remain unchanged.

Validation record on **February 24, 2026** (`dashboard/`):
- ✅ `npm run typecheck`
- ✅ `npm test`
- ✅ `npm run build`

Focused regression suites:
- ✅ `src/pages/ApprovalsPage.test.tsx`
- ✅ `src/components/approvals/ApprovalCardV2.test.tsx`
- ✅ `src/pages/JobsPage.test.tsx`
- ✅ `src/pages/RunsPage.test.tsx`
- ✅ `src/components/StatusBadge.test.tsx`
- ✅ `src/lib/status.test.ts`

Intentional deviations:
- Runs status chips stay compact semantic badges (instead of large glyph badges) to preserve table scan density.
- Approval detail remains a right-side panel on narrow viewports to preserve existing review ergonomics.
- Pipeline stage counts continue current derived safety-stage estimation to avoid backend contract changes in this parity pass.

Intentional deviation risk/rollback register:

| Deviation | Risk | Rollback trigger | Rollback path |
|---|---|---|---|
| Compact semantic Runs status badges (vs large glyph badges) | Low visual-only variance | QA/operator feedback indicates reduced scanability | Restore larger glyph badge variant in Runs rows and rerun operations parity suites |
| Right-side Approvals detail panel on narrow viewports | Low UX variance | Mobile review usability findings indicate friction | Re-enable responsive split-pane detail treatment and revalidate URL/workflow behavior |
| Derived pipeline stage estimate in Runs funnel | Medium metric-presentation risk | Backend publishes canonical stage counters or QA requires strict metric parity | Switch funnel calculations to backend counters and update tests/docs |
| `bg-accent text-white` quick-range active chip (allowlisted) | Low and localized | Accent foreground utility token becomes available | Replace with tokenized foreground utility and remove allowlist in `design-parity.test.ts` |

## Parity validation evidence workflow (task-eab4b87f)

Use `dashboard/DESIGN_PARITY_CHECKLIST.md` as the route-by-route evidence source of truth.

Validation references for every parity run:
- manuscript routes: `/`, `/dashboard`, `/components`, `/colors`, `/typography`
- local artifact comparators: `example/Layout.tsx`, `example/DashboardExample.tsx`, `example/index.css`

Reproducible command sequence (`dashboard/`):

```bash
npm run test -- src/styles/design-parity.test.ts src/styles/theme-tokens.test.ts
npm run test -- src/components/layout/AppShell.test.tsx src/pages/SecurityOverviewPage.test.tsx src/pages/ApprovalsPage.test.tsx src/pages/JobsPage.test.tsx src/pages/RunsPage.test.tsx src/components/ui/StatusIndicator.test.tsx
npm run typecheck
npm test
npm run build
```

Go/no-go review criteria:
- AppShell, Security Overview, Approvals, Jobs, Runs, and shared primitives are all marked PASS in the evidence matrix.
- No unresolved high-severity visual parity deltas.
- Full validation command sequence passes.

Current unresolved delta register:
- None (as of **February 24, 2026**).

## Parity maintenance guide (Windows/macOS/Linux)

When modifying Shell, Security, Approvals, Jobs, Runs, or shared primitive visuals:

1. Update implementation tokens/components (`src/styles/index.css`, `tailwind.config.cjs`, `src/components/ui/*`, `src/components/StatusBadge.tsx`, `src/lib/status.ts`, `src/lib/format.ts`).
2. Update parity guard and accessibility/page tests.
3. Update parity evidence docs (`dashboard/DESIGN_PARITY_CHECKLIST.md`, `dashboard/DESIGN_LANGUAGE_MAPPING.md`, `dashboard/README.md`, `wiki/Dashboard.md`).
4. Record intentional deltas with rationale/risk/rollback before merge.

Validation commands (PowerShell, Bash, or zsh):

```bash
cd dashboard
npm run test -- src/styles/design-parity.test.ts src/styles/theme-tokens.test.ts
npm run test -- src/components/layout/AppShell.test.tsx src/pages/SecurityOverviewPage.test.tsx src/pages/ApprovalsPage.test.tsx src/pages/JobsPage.test.tsx src/pages/RunsPage.test.tsx src/components/ui/StatusIndicator.test.tsx
npm run typecheck
npm test
npm run build
```
