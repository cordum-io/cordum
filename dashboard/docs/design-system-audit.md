# Dashboard design-system audit
_Updated: 2026-04-20 — broader convergence sweep across MCP, P1, the priority P2 route cluster, and the remaining detail/admin pages, including the reopen fix for schema/job-detail drift._

## Convergence progress (task-16ceda44)
- **P0 pilot — `pages/SettingsMcpPage.tsx`** — DONE. Composes only shared primitives (`PageHeader`, `InstrumentCard`, `Tabs`, `CollapsibleSection`, `EmptyState`, `ErrorBanner`, `SkeletonCard`, `Button`, `StatusBadge`). Page-local composition extracted to `components/settings/McpSummaryTiles.tsx` + `McpServerPanel.tsx`. Tests green (18 cases across pilot + primitives).
- **P1 sweep — `pages/ApprovalsPage.tsx`, `pages/AuditLogPage.tsx`, `pages/SettingsUsersPage.tsx`** — IN PROGRESS / largely migrated. These pages now share `StatTile`, `Tabs`, `Input`, `Select`, `Textarea`, `LabeledField`, `Button`, and `StatusBadge` instead of page-local KPI cards, filter bars, and dialog field markup. Remaining drift is now mostly limited to checkbox and table-shell cleanup. (`pages/DLQPage.tsx` was deleted in commit `45dacbbf` after Phase 3 wk4 redirected `/dlq` → `/jobs?status=dlq`; see "DLQ fold status" below.)
- **AuditLogPage hero rewrite — DONE (task-55f813b3, Phase 3 wk3, 2026-05-09).** Promoted out of the P1 sweep into the v2.5 hero set. Now composes `primitives/DataTable` with virtualization at >100 rows and 3px decision-identity left edge, all filters serialised to URL via `nuqs` + `@/lib/url-state` parsers, sticky `ChainIntegrityWidget compact` Merkle bar at top, and a row-click `Drawer` drilldown that derives per-event chain-signature verdict (Verified | Tamper detected | Retention-trimmed | Not chain-signed) from the cached `useAuditVerify` result — opening N drawers fires at most one /audit/verify request via React Query's shared cache.
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

### A11y test status (Phase 5a — task-bf55ddbd, 2026-05-09)

`renderWithProviders` now accepts an opt-in `runAxe: true` option that
delegates to `assertNoSeriousAxeViolations` and asserts no critical/serious
WCAG 2 AA violations on the rendered container. The dedicated
`*.a11y.test.tsx` files (HomePage, SettingsHubPage, PolicyOverviewPage,
InputRuleEditorDrawer) remain the canonical pattern for pages whose first
paint depends on async data; the new opt-in is a sugar layer for primitive +
component tests that don't need a `waitFor` preamble.

| Primitive / surface | A11y status | Source |
| --- | --- | --- |
| `HomePage` | PASS (light + dark) | `pages/HomePage.a11y.test.tsx` |
| `SettingsHubPage` | PASS (light + dark) | `pages/SettingsHubPage.a11y.test.tsx` |
| `PolicyOverviewPage` | PASS (light + dark) | `pages/govern/PolicyOverviewPage.a11y.test.tsx` |
| `InputRuleEditorDrawer` (read-only) | PASS (Escape closes) | `components/policy/input-rules/InputRuleEditorDrawer.a11y.test.tsx` |
| `UserMenu` (closed-menu) | PASS (axe via `runAxe: true`) | `components/UserMenu.test.tsx` (Phase 5a opt-in demo) |
| Other shared primitives | PENDING | extend with `runAxe: true` in their existing test files |

CI gate: `pnpm run lint:a11y` (uses `eslint.a11y.config.mjs`) — must exit 0
on every dashboard PR.
## Audit summary
- Page components reviewed: **46** (45 after DLQPage deletion).
- Pages using `PageHeader`: **37**
- Pages already using `InstrumentCard` or the `.instrument-card` surface: **34**
- Pages still containing raw `<input>/<select>/<textarea>` markup: **2** (down from 27 after reopen #1 + reopen #2 raw-control sweeps; only `LoginPage` and the full-bleed `RunDetailPage` console remain, both explicitly carved out below).
- Pages still carrying raw CSS-var styling / fallback color strings: **6** (down from 27 after the v2.5 drift sweep — only AgentDetailPage, HomePage, RunDetailPage, LoginPage, plus two unrelated low-impact carriers remain; see "v2.5 drift sweep close-out" section below).
- Pages already depending on `MetricValue`: **5**
- Pages already depending on `Tabs` or custom tablist markup: **6**

## v3-overlay close-out (task-dd5e1d8f → task-6fccc637, 2026-05-09)

`WorkflowNodeGovernanceOverlay` ships with all three indicators populated end-to-end:

1. **policy-gate Shield icon** — `step.policyGate` flows from `BackendWorkflowStep.policy_gate` (cordum-core task-913b6c6c) through `mapWorkflowStep` (task-6fccc637 commit `85f0f1e3`) into `UnifiedNodeData.policyGate`. Renders muted in design-time, saturated when the node has a run overlay.
2. **safety-decision badge** — `step.output.safetyDecision` flows through `graphBridge` (task-dd5e1d8f reopen #2 commit `8fe1d412`). Already in service.
3. **audit-hash chip** — `step.auditHash` flows from `BackendStepRun.audit_hash` (cordum-core task-913b6c6c) through `mapWorkflowRunStep` (task-6fccc637 commit `85f0f1e3`). Click-to-copy, 8-char preview. Renders muted placeholder when the run-step record's audit_hash is `null` (e.g. older runs pre-task-913b6c6c or before task-a45b8eb1 backfills the SIEMEvent join).

Initial component shape was carved out in task-dd5e1d8f as "safetyDecision-only ships now; policyGate + auditHash placeholders mark `data-pending-api='task-913b6c6c'` until the cordum-core API additions land". That carve-out is now closed.

Test coverage: `WorkflowNodeGovernanceOverlay.test.tsx` covers component render contract (3 policyGate variants, auditHash render+copy, design-time vs runtime saturation). `transform.test.ts` covers wire-format → dashboard-shape mapping (3 audit_hash cases + 4 policy_gate cases) — locks the contract that closed the loop in task-6fccc637.

## v2.5 drift sweep close-out (task-100cc89c, 2026-05-08 + reopen #1 2026-05-09)

Seven pages newly converged in this sweep, removing **~28 page-local `var(--color-*)` literals**. Commits on PR #249: `b5013067`, `bd9cf670`, `10ff0af1` (mid-sweep correction — see lesson below), `27b658e3`, `b5488d6f`. Each newly converged page has a regression case in `dashboard/src/pages/DesignSystemConvergence.test.ts` asserting `not.toMatch(/var\(--color-/)`.

**Reopen #1 (2026-05-09): raw-control replacement sweep.** QA flagged that pages documented as "converged" still rendered raw `<input>/<select>/<textarea>` controls (DoD #1) and that the new convergence tests only guarded `var(--color-*)` and not the raw-control regex (DoD #2). Five pages had their 22 raw native controls swapped for the canonical `Input` / `Select` / `Textarea` / `Checkbox` / `LabeledField` primitives:

- `pages/govern/ReplayPage.tsx`: 11 raw controls → primitives (search input, direction `<select>`, datetime-local From/To/Max-jobs filters wrapped in `LabeledField`, Tenant + Topic-pattern inputs, Original-decision `<select>`, "Use current published policy" checkbox, candidate-policy YAML `<textarea>`).
- `pages/govern/InputRulesPage.tsx`: 6 raw controls → primitives (decision filter `<select>`, bundle filter `<select>`, search input, context-evaluator Tenant + Topic + Capability inputs).
- `pages/govern/OutputRulesPage.tsx`: 1 raw `<select>` (Bundle) → `Select` primitive.
- `pages/govern/PolicyAnalyticsPage.tsx`: 3 raw inputs → primitives (search input, From + To datetime-local inputs wrapped in `LabeledField`).
- `pages/govern/QuarantinePage.tsx`: 1 raw search input → `Input` primitive with `icon` prop.

`DesignSystemConvergence.test.ts` extended with a `RAW_CONTROL_RE = /<(input|select|textarea)\b/` regex applied per page (5 new test cases). Word-boundary anchored so identifiers/comments/prop names containing the literal word "input" / "select" / "textarea" do not trigger the assertion — only JSX tags do.

**Reopen #2 (2026-05-09): full-scope stale-audit raw-control close-out.** QA re-ran the same case-sensitive raw-control grep against the broader pages that this document claimed were already/silently converged. Those stale claims are now closed: `ApprovalDetailPage`, `TenantDetailPage`, `SimulatorPage`, `BundlesPage`, `TenantsPage`, `VelocityRulesPage`, `WorkflowsPage`, `SettingsSSOPage`, `EdgeSessionsPage`, and `EdgeSessionDetailPage` now compose `Input`, `Select`, `Textarea`, `Checkbox`, and/or `LabeledField` primitives instead of native form fields. The convergence test now includes 10 additional raw-control assertions for these pages. A fresh grep leaves only the explicit carve-outs (`LoginPage` pre-auth + `RunDetailPage` full-bleed console).

**Converged in v2.5 drift sweep:**
- `pages/ApprovalsPage.tsx` — gated card borders + denied-icon → `statusToneTextClasses.governance` / `statusToneBorderClasses.warning|.governance` helpers from `StatusBadge.tsx`.
- `pages/govern/BundleDetailPage.tsx` — unsaved-changes indicator → `statusToneTextClasses.warning` helper.
- `pages/govern/OutputRulesPage.tsx` — viewer-mode banner → `<InfoBanner variant="warning">` primitive.
- `pages/govern/ReplayPage.tsx` — replay link → `text-cordum`.
- `pages/govern/InputRulesPage.tsx` — workflow scope-pill + scope-card → `bg-info/15 text-info`.
- `pages/govern/PolicyAnalyticsPage.tsx` — bar fill, override highlights, false-positive banner → `fill-cordum`, `text-warning`, `bg-warning/5`, `text-cordum` bare-token classes.
- `pages/govern/QuarantinePage.tsx` — severity-tone helpers, finding-dot indicator, medium-tier card border → `text-warning`, `text-info`, `bg-warning`, `bg-info`, `border-warning/20`.

**Pages discovered already silently converged on grep (audit text was stale)**: `pages/SettingsNotificationsPage.tsx`, `pages/govern/PolicyOverviewPage.tsx`. Earlier stale claims for `ApprovalDetailPage`, `TenantDetailPage`, `SimulatorPage`, `BundlesPage`, `TenantsPage`, and `VelocityRulesPage` were corrected by reopen #2's primitive migration above.

**Pages deliberately retained with `var(--color-*)`:**
- `pages/AgentDetailPage.tsx` — recharts `<Bar fill="var(--color-cordum)" />` props are theme-bound (chart-fill drift cannot be Tailwind-converted without breaking the theme reference). Decision-identity exception, mirrors HomePage's chart palette discipline (msg-96e66aaa).
- `pages/HomePage.tsx` — Phase 3 hero rewrite scope per epic plan; the AreaChart kept decision-identity tokens (success/governance/warning/danger) by design.
- `pages/LoginPage.tsx` — pre-auth surface, low-impact, out of v2.5 scope.
- `pages/RunDetailPage.tsx` — existing DoD-3 carve-out (full-bleed canvas console UX); the carve-out now extends to its raw-controls/raw-vars signals as well.

**Lesson saved as memory mem-541413bc**: the codebase's Tailwind `@theme inline` block in `src/styles/index.css` registers `--color-warning` / `--color-governance` / `--color-info` / `--color-success` / `--color-cordum` (no `status-` prefix). The canonical Tailwind utilities are `text-warning`, `bg-info/15`, `border-governance/20`, `fill-cordum`, etc. `text-status-warning` only exists as a `.instrument-card.status-warning::before` state class — using it on other elements is a no-op visually (Tailwind silently ignores). Two batches of this sweep initially used `text-status-*` and produced visually-broken output; commit `10ff0af1` corrected by switching to `statusToneTextClasses` / `statusToneBorderClasses` helpers from `StatusBadge.tsx` plus the `<InfoBanner>` primitive.

**DLQ fold status (task-0bcb9411 + task-100cc89c step 5, 2026-05-09)**: Phase 3 wk4 follow-up landed the prerequisite for deleting the standalone `DLQPage.tsx`: `/dlq` now redirects to `/jobs?status=dlq`, the AppShell Dead Letters item targets the same JobsPage filter, and JobsPage swaps to the DLQ data source with inline Replay/Drop actions when that filter is active. `DLQPage.tsx` and the `components/dlq/` subtree (`DLQActions.tsx`, `RetryAttemptsPanel.tsx`) plus the page test file were deleted in commit `45dacbbf` (-940 LOC); the `/dlq` redirect route in `App.tsx` is the only thing that remains. CommandPalette's Dead Letters entry repoints to `/jobs?status=dlq`.
## Drift signals used in this audit
- **Raw inputs** — page renders native form fields instead of central control primitives.
- **Raw CSS vars** — page uses fallback `var(--...)` styling or hard-coded surface wrappers instead of design-system primitives.
- **Low primitive reuse** — page already has equivalent shared components available but still builds bespoke KPI, tab, disclosure, empty, or error markup locally.
## Priority backlog
### P0 — migrate now
- **`pages/SettingsMcpPage.tsx`** — biggest gap against the shared system. It hand-rolls KPI cards, the servers/analytics tab strip, expansion rows, and several loading/disabled/empty states while equivalent building blocks already exist.
### P1 — next cleanup wave after the MCP pilot

Each entry below carries a concrete checklist so the next worker can continue the sweep without re-auditing.

- ~~**`pages/DLQPage.tsx`**~~ — DELETED in task-100cc89c step 5 (commit `45dacbbf`) after Phase 3 wk4's DLQ fold (task-0bcb9411) made `/dlq` redirect to `/jobs?status=dlq`. JobsPage owns the DLQ surface end-to-end now (filter, fixtures, bulk actions, Replay/Drop). See "DLQ fold status" above.
- **`pages/ApprovalsPage.tsx`** — KPI row, search, tabs, and denial note now use `StatTile`, `Input`, `Tabs`, and `Textarea`. v2.5 drift sweep (commit `b5013067` + `10ff0af1`) converged the gated card borders + denied-icon onto `statusToneTextClasses` / `statusToneBorderClasses` helpers. Remaining drift: convert the legacy drawer shell to `Drawer`/`CollapsibleSection` primitives and replace the raw lifecycle-note warning block with a reusable info/warning banner pattern (deferred to a follow-up — drawer migration is a11y-sensitive).
- **`pages/AuditLogPage.tsx`** — DONE in the Phase 3 wk3 hero rewrite (task-55f813b3, 2026-05-09). Hand-rolled `<table>` + `motion.tbody` infinite-scroll replaced with `primitives/DataTable` (virtualization auto-engages above the 100-row threshold; `decisionAccessor` paints the 3px left edge per safety-decision tier). Filters migrated to `nuqs` URL state via `parseAsSearchTerm` + `parseAsString.withDefault('')` for action/agent/from/to (URL roundtrip confirmed by Block A tests). Sticky `ChainIntegrityWidget compact` Merkle bar mounted at top. Row-click opens a `Drawer` with `<AuditEventDrilldown>` rendering event metadata (DrillRow primitive with copy buttons) + `<ChainSignatureSection>` deriving per-event signature verdict from the cached `useAuditVerify` chain-wide result.
- **`pages/SettingsUsersPage.tsx`** — summary row, tab switcher, search, dialog forms, and permission toggles now use `StatTile`, `Tabs`, `Input`, `Select`, `LabeledField`, and `Checkbox`. Remaining drift: decide whether the role cards should converge on `CollapsibleSection` or intentionally stay card-based.
### P2 — medium-priority convergence
- DONE in the detail/admin sweep: `pages/JobDetailPage.tsx`, `pages/settings/SettingsSSOPage.tsx`, `pages/settings/SettingsSCIMPage.tsx`, `pages/settings/LicensePage.tsx`, `pages/SchemaDetailPage.tsx`, `pages/SchemasPage.tsx`
- DONE in the v2.5 drift sweep (task-100cc89c, 2026-05-08): `pages/govern/BundleDetailPage.tsx`, `pages/govern/OutputRulesPage.tsx`, `pages/govern/ReplayPage.tsx`, `pages/govern/InputRulesPage.tsx`, `pages/govern/PolicyAnalyticsPage.tsx`, `pages/govern/QuarantinePage.tsx`. See "v2.5 drift sweep close-out" section above.
- DONE in the v2.5 drift sweep reopen #2 (raw-control close-out, 2026-05-09): `pages/approvals/ApprovalDetailPage.tsx`, `pages/govern/TenantDetailPage.tsx`, `pages/govern/SimulatorPage.tsx`, `pages/govern/BundlesPage.tsx`, `pages/govern/TenantsPage.tsx`, `pages/govern/VelocityRulesPage.tsx`, `pages/WorkflowsPage.tsx`, `pages/settings/SettingsSSOPage.tsx`, `pages/EdgeSessionsPage.tsx`, `pages/EdgeSessionDetailPage.tsx`.
- Already silently converged (audit text was stale, grep on 2026-05-09 returned 0 drift signals): `pages/SettingsNotificationsPage.tsx`, `pages/govern/PolicyOverviewPage.tsx`.
- `pages/RunDetailPage.tsx` carve-out: existing DoD-3 exemption now extends to raw-controls/raw-vars signals (full-bleed canvas console UX).
### Bugs / cleanup notes noticed during the audit
- `components/settings/SettingsLayout.tsx` and `components/KeyboardShortcutsHelp.tsx` were flagged early in the audit for old token naming. Re-check these files before close-out to ensure no stale `surface2` references remain after the broader sweep is merged.
## Full page matrix
| Area | Page | Signals detected | Priority |
| --- | --- | --- | --- |
| Operate | `pages/AgentDetailPage.tsx` | PageHeader, InstrumentCard, ErrorBanner, Raw CSS vars | P3/P4 |
| Operate | `pages/AgentIdentityDetailPage.tsx` | PageHeader, InstrumentCard, ErrorBanner, Motion | P3/P4 |
| Operate | `pages/AgentsPage.tsx` | PageHeader, StatTile, Tabs, Input, EmptyState, ErrorBanner, Motion | Converged in priority P2 sweep |
| Approvals | `pages/approvals/ApprovalDetailPage.tsx` | PageHeader, ErrorBanner, Textarea, Motion | Converged in v2.5 drift sweep reopen #2 |
| Orchestrate | `pages/ApprovalsPage.tsx` | PageHeader, StatTile, Tabs, Input, Textarea, EmptyState, Motion | P1 (mostly migrated) |
| Observe | `pages/AuditLogPage.tsx` | PageHeader, InstrumentCard, LabeledField, Input, Select, StatusBadge, EmptyState, ErrorBanner, DataTable, Drawer, ChainIntegrityWidget(compact), nuqs URL state | DONE (Phase 3 wk3, task-55f813b3) |
| Observe | ~~`pages/DLQPage.tsx`~~ | DELETED — folded into JobsPage `?status=dlq` (task-0bcb9411 + task-100cc89c step 5, commit `45dacbbf`) | DONE |
| Govern | `pages/govern/BundleDetailPage.tsx` | PageHeader, InstrumentCard, EmptyState, Raw CSS vars | P3/P4 |
| Govern | `pages/govern/BundlesPage.tsx` | PageHeader, InstrumentCard, MetricValue, Select, EmptyState | Converged in v2.5 drift sweep reopen #2 |
| Govern | `pages/govern/InputRulesPage.tsx` | PageHeader, InstrumentCard, EmptyState, Raw inputs, Raw CSS vars | P3/P4 |
| Govern | `pages/govern/OutputRulesPage.tsx` | PageHeader, EmptyState, Raw inputs, Raw CSS vars | P3/P4 |
| Govern | `pages/govern/PolicyAnalyticsPage.tsx` | PageHeader, EmptyState, Raw inputs, Raw CSS vars, Motion | P3/P4 |
| Govern | `pages/govern/PolicyOverviewPage.tsx` | PageHeader, Raw CSS vars | P3/P4 |
| Govern | `pages/govern/QuarantinePage.tsx` | PageHeader, InstrumentCard, MetricValue, EmptyState, Raw inputs, Raw CSS vars, Motion | P3/P4 |
| Govern | `pages/govern/ReplayPage.tsx` | PageHeader, InstrumentCard, EmptyState, Raw inputs, Raw CSS vars, Motion | P3/P4 |
| Govern | `pages/govern/SimulatorPage.tsx` | PageHeader, InstrumentCard, Select, EmptyState | Converged in v2.5 drift sweep reopen #2 |
| Govern | `pages/govern/TenantDetailPage.tsx` | PageHeader, Input, Select, LabeledField, EmptyState | Converged in v2.5 drift sweep reopen #2 |
| Govern | `pages/govern/TenantsPage.tsx` | PageHeader, InstrumentCard, MetricValue, Select, EmptyState | Converged in v2.5 drift sweep reopen #2 |
| Govern | `pages/govern/VelocityRulesPage.tsx` | PageHeader, InstrumentCard, Input, Select, Checkbox, Textarea, EmptyState, ErrorBanner | Converged in v2.5 drift sweep reopen #2 |
| Operate | `pages/HomePage.tsx` | PageHeader, StatTile, primitives/DataTable, InstrumentCard, ErrorBanner, CollapsibleSection, Motion, --chart-1..5 tokens | Converged in Phase 3 wk5 (task-5101a23c) |
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
| Settings | `pages/settings/SettingsSSOPage.tsx` | PageHeader, InstrumentCard, DetailList, Input, Textarea, LabeledField, InfoBanner, StatusBadge, ErrorBanner, Motion | Converged in detail/admin sweep + reopen #2 raw-control close-out |
| Settings | `pages/SettingsConfigPage.tsx` | PageHeader, Tabs, Input, Select, Checkbox, LabeledField, InfoBanner, ErrorBanner, Motion | Converged in priority P2 sweep |
| Settings | `pages/SettingsEnvironmentsPage.tsx` | PageHeader, InstrumentCard, EmptyState, ErrorBanner, Motion | P3/P4 |
| Settings | `pages/SettingsHealthPage.tsx` | PageHeader, InstrumentCard, Motion | P3/P4 |
| Settings | `pages/SettingsHubPage.tsx` | PageHeader, InstrumentCard, Motion | P3/P4 |
| Settings | `pages/SettingsKeysPage.tsx` | PageHeader, EmptyState, ErrorBanner, Input, Checkbox, LabeledField, InfoBanner, Motion | Converged in priority P2 sweep |
| Settings | `pages/SettingsMcpPage.tsx` | PageHeader, InstrumentCard, Tabs, Raw CSS vars, Motion | P0 pilot |
| Settings | `pages/SettingsNotificationsPage.tsx` | PageHeader, InstrumentCard, ErrorBanner, Motion | P3/P4 |
| Settings | `pages/SettingsUsersPage.tsx` | PageHeader, StatTile, InstrumentCard, Tabs, LabeledField, Input, Select, EmptyState, ErrorBanner, Motion | P1 (mostly migrated) |
| Extend | `pages/TopicsPage.tsx` | PageHeader, StatTile, EmptyState, ErrorBanner, StatusBadge, Motion | Converged in priority P2 sweep |
| Orchestrate | `pages/WorkflowsPage.tsx` | PageHeader, InstrumentCard, Input, EmptyState, ErrorBanner, Motion | Converged in v2.5 drift sweep reopen #2 |
| Orchestrate | `pages/WorkflowStudioPage.tsx` | Motion | P3/P4 |
