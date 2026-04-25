# Dashboard design-system audit
_Updated: 2026-04-20 — broader convergence sweep across MCP, P1, the priority P2 route cluster, and the remaining detail/admin pages, including the reopen fix for schema/job-detail drift._

## Convergence progress (task-16ceda44)
- **P0 pilot — `pages/SettingsMcpPage.tsx`** — DONE. Composes only shared primitives (`PageHeader`, `InstrumentCard`, `Tabs`, `CollapsibleSection`, `EmptyState`, `ErrorBanner`, `SkeletonCard`, `Button`, `StatusBadge`). Page-local composition extracted to `components/settings/McpSummaryTiles.tsx` + `McpServerPanel.tsx`. Tests green (18 cases across pilot + primitives).
- **P1 sweep — `pages/ApprovalsPage.tsx`, `pages/AuditLogPage.tsx`, `pages/SettingsUsersPage.tsx`, `pages/DLQPage.tsx`** — IN PROGRESS / largely migrated. These pages now share `StatTile`, `Tabs`, `Input`, `Select`, `Textarea`, `LabeledField`, `Button`, and `StatusBadge` instead of page-local KPI cards, filter bars, and dialog field markup. Remaining drift is now mostly limited to checkbox and table-shell cleanup.
- **Priority P2 cluster — `pages/JobsPage.tsx`, `pages/PacksPage.tsx`, `pages/SettingsKeysPage.tsx`, `pages/SettingsConfigPage.tsx`, `pages/settings/SettingsAuditExportPage.tsx`, `pages/AgentsPage.tsx`, `pages/TopicsPage.tsx`** — DONE for primitive convergence. This sweep removed the raw search fields, tab strips, dialog field markup, warning blocks, and token-only KPI tiles on the targeted pages. The cluster now composes shared `Tabs`, `Input`, `Select`, `Textarea`, `LabeledField`, `Checkbox`, `StatTile`, `DialogOverlay`, `InfoBanner`, `Button`, and `StatusBadge`, and the seven route files no longer contain raw `<input>/<select>/<textarea>` markup or page-local `var(--...)` color treatment.
- **Detail/admin P2 cluster — `pages/JobDetailPage.tsx`, `pages/settings/SettingsSSOPage.tsx`, `pages/settings/SettingsSCIMPage.tsx`, `pages/settings/LicensePage.tsx`, `pages/SchemaDetailPage.tsx`, `pages/SchemasPage.tsx`** — DONE after the reopen fix. `SchemaDetailPage` now keeps its create/editor surface on shared `Input`, `Select`, `Checkbox`, `LabeledField`, `Button`, and `Tabs` primitives; `SchemasPage` search is back on the shared `Input` search-field treatment; and `JobDetailPage` status/timeline chrome no longer carries page-local `var(--color-*)` classes, instead reusing shared status-tone tokens exported from the `StatusBadge` primitive. Remaining route drift is now concentrated in deeper govern/detail surfaces (`ApprovalDetail`, `BundleDetail`, `TenantDetail`, `RunDetail`, `SettingsNotifications`, etc.) rather than the main operator/settings/admin cluster.
- **Verification snapshot** — run after the broader sweep and reopen fix: `npm run typecheck`, targeted Vitest for shared primitives and touched route logic, and a convergence regression test that mechanically guards the scoped schema/job-detail files against raw controls and page-local `var(--color-*)` styling.
## DoD-3 (12-col Bento Grid) — exemptions
Premium Overhaul DoD-3 says detail pages compose on `lg:grid-cols-12` with heterogeneous col-span tiles. This register carves out pages whose UX is structurally incompatible with a bento dashboard.

**Exempted pages:**
- `src/pages/RunDetailPage.tsx` — real-time workflow-run inspection console. Full-viewport fixed-height shell (`h-[calc(100vh-64px)] -m-6` at line 462) breaks out of AppShell padding. Three-pane interaction model: step-graph sidebar + step-output accordion + chat panel + governance tab. Not a scrollable bento dashboard — stapling `lg:grid-cols-12` onto the flex root would be cosmetic-only and would not fit the console UX.

**Not exempted (still on the hook):**
- `src/pages/govern/BundleDetailPage.tsx` — tracked by its own DoD-3 + DoD-2 refactor task. Do not extend this carve-out to it.

Decided 2026-04-24 · task-c154ff08 · epic-2e0ed1ee.

## Motion tokens
- `--duration-soft: 250ms` is the Soft Control Surface transition speed declared in `dashboard/src/styles/index.css` (light + dark themes) and aliased in the `@theme` block as `--animate-duration-soft`. Consumers: `components/ui/Button.tsx` and `components/ui/Card.tsx` via the Tailwind JIT arbitrary-value form `duration-[var(--duration-soft)]`. Pinned by DoD-1 token-declaration assertion (`design tokens shadow-soft, --radius 0.75rem, duration-soft exist for light and dark`) and DoD-2 consumer assertions (`Button consumes --duration-soft token` + `Card consumes --duration-soft token`) in `src/pages/DesignSystemConvergence.test.ts`. Adoption landed in commit 1b95ac65 (task-bd7eb4af Soft UI Evolution); orphan-token gap closed under task-ed23bcf5.

## Governance surfaces
- **Chain integrity monitoring** is mounted at `/govern/verification` (admin-only, gated by `<RequireRole roles={["admin"]}>`). The PolicyOverviewPage was simplified on 2026-04-24 (Level 3 sweep, commit 046914d9); the chain-integrity widget is no longer embedded in the Overview tab but remains reachable via the Verification route in the Govern nav section. Non-admin viewers see a friendly EmptyState fallback on the Verification page (not a blank card). Restored under task-14d012e6.

## Scope
- Reviewed `cordum/dashboard/src/pages/**/*.tsx` (non-test page components only) and cross-checked against the current route surface in `src/App.tsx`.
- Focused on whether each page composes central layout/UI primitives or re-introduces page-local panels, tabs, filters, fields, and state blocks.
- This audit is the source-of-truth backlog for the design-system convergence epic; `/settings/mcp` is the initial pilot.
## Existing central primitives worth reusing
- Layout: `PageHeader`, `AppShell`
- Panels: `InstrumentCard`, `Card`, `.instrument-card`, `.surface-card`, `.list-row`
- Controls: `Button`, `Input`, `Select`, `Textarea`, `ComboboxInput`, `TagInput`
- State + disclosure: `EmptyState`, `ErrorBanner`, `SkeletonCard`, `CollapsibleSection`, `StatusBadge`
- Metrics/navigation: `MetricValue`, `StatTile`, `Tabs`, `Pagination`, `DataTable`
- Field wrappers: `LabeledField`
## Audit summary
- Page components reviewed: **46**
- Pages using `PageHeader`: **37**
- Pages already using `InstrumentCard` or the `.instrument-card` surface: **34**
- Pages still containing raw `<input>/<select>/<textarea>` markup: **27**
- Pages still carrying raw CSS-var styling / fallback color strings: **27**
- Pages already depending on `MetricValue`: **5**
- Pages already depending on `Tabs` or custom tablist markup: **6**
## Drift signals used in this audit
- **Raw inputs** — page renders native form fields instead of central control primitives.
- **Raw CSS vars** — page uses fallback `var(--...)` styling or hard-coded surface wrappers instead of design-system primitives.
- **Low primitive reuse** — page already has equivalent shared components available but still builds bespoke KPI, tab, disclosure, empty, or error markup locally.
## Priority backlog
### P0 — migrate now
- **`pages/SettingsMcpPage.tsx`** — biggest gap against the shared system. It hand-rolls KPI cards, the servers/analytics tab strip, expansion rows, and several loading/disabled/empty states while equivalent building blocks already exist.
### P1 — next cleanup wave after the MCP pilot

Each entry below carries a concrete checklist so the next worker can continue the sweep without re-auditing.

- **`pages/DLQPage.tsx`** — KPI row + search + error state + bulk actions are now on shared primitives, and row selection now uses the shared `Checkbox` primitive. Remaining work:
  - Replace the bespoke `instrument-card status-danger` table wrapper with a composition of `InstrumentCard` + an internal `DataTable` once the floating-action-bar rail fits the new primitive.
- **`pages/ApprovalsPage.tsx`** — KPI row, search, tabs, and denial note now use `StatTile`, `Input`, `Tabs`, and `Textarea`. Remaining drift: convert the legacy drawer shell to `Drawer`/`CollapsibleSection` primitives and replace the raw lifecycle-note warning block with a reusable info/warning banner pattern.
- **`pages/AuditLogPage.tsx`** — filter row and action chips now use `Input`, `Select`, `LabeledField`, `Button`, and `StatusBadge`. Remaining drift: convert the table shell to `DataTable`/`Pagination` once the infinite-scroll behaviour is abstracted.
- **`pages/SettingsUsersPage.tsx`** — summary row, tab switcher, search, dialog forms, and permission toggles now use `StatTile`, `Tabs`, `Input`, `Select`, `LabeledField`, and `Checkbox`. Remaining drift: decide whether the role cards should converge on `CollapsibleSection` or intentionally stay card-based.
### P2 — medium-priority convergence
- DONE in the detail/admin sweep: `pages/JobDetailPage.tsx`, `pages/settings/SettingsSSOPage.tsx`, `pages/settings/SettingsSCIMPage.tsx`, `pages/settings/LicensePage.tsx`, `pages/SchemaDetailPage.tsx`, `pages/SchemasPage.tsx`
- Next backlog concentration: `pages/approvals/ApprovalDetailPage.tsx`, `pages/govern/BundleDetailPage.tsx`, `pages/govern/TenantDetailPage.tsx`, `pages/RunDetailPage.tsx`, and the remaining govern/listing surfaces still carrying raw controls or token-only state wrappers.
### Bugs / cleanup notes noticed during the audit
- `components/settings/SettingsLayout.tsx` and `components/KeyboardShortcutsHelp.tsx` were flagged early in the audit for old token naming. Re-check these files before close-out to ensure no stale `surface2` references remain after the broader sweep is merged.
## Full page matrix
| Area | Page | Signals detected | Priority |
| --- | --- | --- | --- |
| Operate | `pages/AgentDetailPage.tsx` | PageHeader, InstrumentCard, ErrorBanner, Raw CSS vars | P3/P4 |
| Operate | `pages/AgentIdentityDetailPage.tsx` | PageHeader, InstrumentCard, ErrorBanner, Motion | P3/P4 |
| Operate | `pages/AgentsPage.tsx` | PageHeader, StatTile, Tabs, Input, EmptyState, ErrorBanner, Motion | Converged in priority P2 sweep |
| Approvals | `pages/approvals/ApprovalDetailPage.tsx` | PageHeader, ErrorBanner, Raw inputs, Motion | P3/P4 |
| Orchestrate | `pages/ApprovalsPage.tsx` | PageHeader, StatTile, Tabs, Input, Textarea, EmptyState, Motion | P1 (mostly migrated) |
| Observe | `pages/AuditLogPage.tsx` | PageHeader, InstrumentCard, LabeledField, Input, Select, StatusBadge, EmptyState, ErrorBanner, Motion | P1 (mostly migrated) |
| Observe | `pages/DLQPage.tsx` | PageHeader, StatTile, Input, Button, EmptyState, ErrorBanner, Motion | P1 (mostly migrated) |
| Govern | `pages/govern/BundleDetailPage.tsx` | PageHeader, InstrumentCard, EmptyState, Raw CSS vars | P3/P4 |
| Govern | `pages/govern/BundlesPage.tsx` | PageHeader, InstrumentCard, MetricValue, EmptyState | P3/P4 |
| Govern | `pages/govern/InputRulesPage.tsx` | PageHeader, InstrumentCard, EmptyState, Raw inputs, Raw CSS vars | P3/P4 |
| Govern | `pages/govern/OutputRulesPage.tsx` | PageHeader, EmptyState, Raw inputs, Raw CSS vars | P3/P4 |
| Govern | `pages/govern/PolicyAnalyticsPage.tsx` | PageHeader, EmptyState, Raw inputs, Raw CSS vars, Motion | P3/P4 |
| Govern | `pages/govern/PolicyOverviewPage.tsx` | PageHeader, Raw CSS vars | P3/P4 |
| Govern | `pages/govern/QuarantinePage.tsx` | PageHeader, InstrumentCard, MetricValue, EmptyState, Raw inputs, Raw CSS vars, Motion | P3/P4 |
| Govern | `pages/govern/ReplayPage.tsx` | PageHeader, InstrumentCard, EmptyState, Raw inputs, Raw CSS vars, Motion | P3/P4 |
| Govern | `pages/govern/SimulatorPage.tsx` | PageHeader, InstrumentCard, EmptyState, Raw inputs | P3/P4 |
| Govern | `pages/govern/TenantDetailPage.tsx` | PageHeader, EmptyState, Raw inputs | P3/P4 |
| Govern | `pages/govern/TenantsPage.tsx` | PageHeader, InstrumentCard, MetricValue, EmptyState, Raw inputs | P3/P4 |
| Govern | `pages/govern/VelocityRulesPage.tsx` | PageHeader, InstrumentCard, EmptyState, ErrorBanner, Raw inputs | P3/P4 |
| Operate | `pages/HomePage.tsx` | PageHeader, InstrumentCard, MetricValue, ErrorBanner, CollapsibleSection, Raw CSS vars, Motion | P3/P4 |
| Operate | `pages/JobDetailPage.tsx` | InstrumentCard, EmptyState, InfoBanner, StatusBadge, CollapsibleSection, Motion | Converged in detail/admin sweep |
| Operate | `pages/JobsPage.tsx` | PageHeader, Tabs, Input, Textarea, LabeledField, EmptyState, ErrorBanner, Motion | Converged in priority P2 sweep |
| Support | `pages/LoginPage.tsx` | Raw inputs, Raw CSS vars, Motion | P3/P4 |
| Support | `pages/NotFoundPage.tsx` | Motion | P3/P4 |
| Extend | `pages/PackDetailPage.tsx` | ErrorBanner | P3/P4 |
| Extend | `pages/PacksPage.tsx` | PageHeader, InstrumentCard, Tabs, Input, EmptyState, ErrorBanner, Motion | Converged in priority P2 sweep |
| Orchestrate | `pages/RunDetailPage.tsx` | EmptyState, Raw inputs, Raw CSS vars, Motion | P3/P4 |
| Extend | `pages/SchemaDetailPage.tsx` | PageHeader, InstrumentCard, Tabs, InfoBanner, ErrorBanner, Motion | Converged in detail/admin sweep |
| Extend | `pages/SchemasPage.tsx` | PageHeader, InstrumentCard, Input, EmptyState, ErrorBanner, Motion | Converged in detail/admin sweep |
| Settings | `pages/settings/InputSafetySettings.tsx` | ErrorBanner | P3/P4 |
| Settings | `pages/settings/LicensePage.tsx` | PageHeader, InstrumentCard, DetailList, StatTile, StatusBadge, ErrorBanner, Motion | Converged in detail/admin sweep |
| Settings | `pages/settings/OutputSafetySettings.tsx` | ErrorBanner, Raw inputs | P3/P4 |
| Settings | `pages/settings/SettingsAuditExportPage.tsx` | PageHeader, InstrumentCard, Tabs, Input, LabeledField, EmptyState, ErrorBanner, Motion | Converged in priority P2 sweep |
| Settings | `pages/settings/SettingsSCIMPage.tsx` | PageHeader, InstrumentCard, DetailList, EmptyState, StatTile, StatusBadge, ErrorBanner, Motion | Converged in detail/admin sweep |
| Settings | `pages/settings/SettingsSSOPage.tsx` | PageHeader, InstrumentCard, DetailList, InfoBanner, StatusBadge, ErrorBanner, Motion | Converged in detail/admin sweep |
| Settings | `pages/SettingsConfigPage.tsx` | PageHeader, Tabs, Input, Select, Checkbox, LabeledField, InfoBanner, ErrorBanner, Motion | Converged in priority P2 sweep |
| Settings | `pages/SettingsEnvironmentsPage.tsx` | PageHeader, InstrumentCard, EmptyState, ErrorBanner, Motion | P3/P4 |
| Settings | `pages/SettingsHealthPage.tsx` | PageHeader, InstrumentCard, Motion | P3/P4 |
| Settings | `pages/SettingsHubPage.tsx` | PageHeader, InstrumentCard, Motion | P3/P4 |
| Settings | `pages/SettingsKeysPage.tsx` | PageHeader, EmptyState, ErrorBanner, Input, Checkbox, LabeledField, InfoBanner, Motion | Converged in priority P2 sweep |
| Settings | `pages/SettingsMcpPage.tsx` | PageHeader, InstrumentCard, Tabs, Raw CSS vars, Motion | P0 pilot |
| Settings | `pages/SettingsNotificationsPage.tsx` | PageHeader, InstrumentCard, ErrorBanner, Motion | P3/P4 |
| Settings | `pages/SettingsUsersPage.tsx` | PageHeader, StatTile, InstrumentCard, Tabs, LabeledField, Input, Select, EmptyState, ErrorBanner, Motion | P1 (mostly migrated) |
| Extend | `pages/TopicsPage.tsx` | PageHeader, StatTile, EmptyState, ErrorBanner, StatusBadge, Motion | Converged in priority P2 sweep |
| Orchestrate | `pages/WorkflowsPage.tsx` | PageHeader, InstrumentCard, EmptyState, ErrorBanner, Raw inputs, Motion | P3/P4 |
| Orchestrate | `pages/WorkflowStudioPage.tsx` | Motion | P3/P4 |
