# v2.5 design-system drift remediation checklist

_Tracks Phase 4 of epic-252d2c07 (Dashboard v2.5 — Hybrid Improvement)._
_Source of truth: `dashboard/docs/design-system-audit.md` (matrix at lines 67–115)._
_Last updated: 2026-05-08 — task-100cc89c step 1._

## Convergence rules (per page)

For every page in scope:
- Replace raw `<input>` / `<select>` / `<textarea>` with `Input` / `Select` / `Textarea` primitives (composing `LabeledField` for form rows).
- Replace page-local `var(--color-*)` styling with shared status-tone tokens emitted by the `StatusBadge` primitive.
- Replace page-local KPI / metric markup with `StatTile` (or `MetricValue` inside an `InstrumentCard` when single-value).
- Replace bespoke tab strips with the `Tabs` primitive.
- Reuse `cn()` from `@/lib/utils` for conditional classes; no new color literals.
- Keep behavior unchanged — this is convergence, not feature work.

For each newly converged page, add an `it()` block in `dashboard/src/pages/DesignSystemConvergence.test.ts` that imports the page source via `?raw` and asserts:
- Source does NOT match the raw-control regex (`/<(input|select|textarea)[\s>]/`).
- Source does NOT match the page-local `var(--color-` regex.
- Source DOES include at least one canonical primitive import (`Input`, `Select`, `StatTile`, `Tabs`, `StatusBadge`, etc., per the page).

## Batch plan

Pages are chunked into 4 PRs. Each PR carries its own dashboard-rail evidence (tsc + vitest + build) and its own convergence-test additions.

### Batch A — Govern detail surfaces (4 pages, customer-facing)

PR commit prefix: `refactor(dashboard): converge govern detail pages on shared primitives (batch A)`

| Page | Drift signals (per audit) | Score | Notes |
|---|---|---|---|
| `pages/govern/BundleDetailPage.tsx` | Raw CSS vars | M | DoD-3 + DoD-2 already tracked; no carve-out. |
| `pages/govern/TenantDetailPage.tsx` | Raw inputs | S | Form controls in admin tabs. |
| `pages/approvals/ApprovalDetailPage.tsx` | Raw inputs, Motion | M | Decision drawer + lifecycle notes. |
| `pages/RunDetailPage.tsx` | Raw inputs, Raw CSS vars, Motion | — | **CARVE OUT.** Already exempted from DoD-3 (full-bleed canvas, line 13–14 of design-system-audit.md). Document the same exemption applies to raw-controls/raw-vars: the workflow inspection console has UX-driven page-local styling. Update audit doc rationale; do NOT force convergence. |

### Batch B — Settings drift (2 pages, finishes Settings polish)

PR commit prefix: `refactor(dashboard): converge settings drift pages (batch B)`

| Page | Drift signals | Score | Notes |
|---|---|---|---|
| `pages/SettingsNotificationsPage.tsx` | InstrumentCard, ErrorBanner, Motion | S | Audit shows minimal signals; verify drift via grep before refactoring (some pages already converged silently). |
| `pages/SettingsUsersPage.tsx` (finish P1) | StatTile, InstrumentCard, Tabs, LabeledField, Input, Select, EmptyState, ErrorBanner, Motion | S | "Mostly migrated" per audit; remaining drift is the role-cards-vs-CollapsibleSection decision. Resolve and lock in. |

### Batch C — Govern Replay/Simulator/Analytics + listings (8 pages)

PR commit prefix: `refactor(dashboard): converge govern replay/sim/analytics + rule listings (batch C)`

| Page | Drift signals | Score | Notes |
|---|---|---|---|
| `pages/govern/ReplayPage.tsx` | Raw inputs, Raw CSS vars, Motion | M | |
| `pages/govern/SimulatorPage.tsx` | Raw inputs | M | |
| `pages/govern/PolicyAnalyticsPage.tsx` | Raw inputs, Raw CSS vars, Motion | M | |
| `pages/govern/PolicyOverviewPage.tsx` | Raw CSS vars | S | Chain-integrity widget already simplified (commit 046914d9); just token swaps. |
| `pages/govern/InputRulesPage.tsx` | Raw inputs, Raw CSS vars | M | |
| `pages/govern/OutputRulesPage.tsx` | Raw inputs, Raw CSS vars | M | |
| `pages/govern/VelocityRulesPage.tsx` | Raw inputs | S | |
| `pages/govern/BundlesPage.tsx` | None new (per audit just MetricValue, EmptyState) | S | Verify-only — may be near-converged. |
| `pages/govern/TenantsPage.tsx` | Raw inputs | S | Listing search input. |

### Batch D — Quarantine + ApprovalsPage finish-P1 (2 pages, more drift to remediate)

PR commit prefix: `refactor(dashboard): finish ApprovalsPage P1 + converge Quarantine (batch D)`

| Page | Drift signals | Score | Notes |
|---|---|---|---|
| `pages/govern/QuarantinePage.tsx` | Raw inputs, Raw CSS vars, Motion | L | Largest single page in this sweep; bulk-action drawer + filter row + KPI cards. |
| `pages/ApprovalsPage.tsx` (finish P1) | StatTile, Tabs, Input, Textarea, EmptyState, Motion | M | Audit lists "remaining drift: drawer shell to Drawer/CollapsibleSection + warning block to InfoBanner". |

## DLQPage deletion (companion step in batch B or its own commit)

Per task-100cc89c step 5: now that Phase 3 wk4 ships `/dlq` → `/jobs?status=dlq` redirect AND the DLQ filter on JobsPage handles DLQ functionality, delete:
- `dashboard/src/pages/DLQPage.tsx`
- `dashboard/src/pages/DLQPage.test.tsx`
- `dashboard/src/components/dlq/` (audit and delete if orphaned)

Update `dashboard/src/components/CommandPalette.tsx`: drop the "dead letters" → `/dlq` link OR repoint to `/jobs?status=dlq`.

Verify `dashboard/src/App.tsx` redirect remains and no other component imports DLQPage.

## Out-of-scope / already-converged pages

These pages appear in the audit P3/P4 column but are NOT part of this sweep. They have minimal drift signals or are intentionally exempted. Flag in the final audit-doc cleanup (step 7) as either "converged" or "carve-out".

| Page | Reason |
|---|---|
| `pages/AgentDetailPage.tsx` | Drift signal "Raw CSS vars" only — re-grep; if still genuine, file follow-up task. Not in step-4 affectedFiles. |
| `pages/AgentIdentityDetailPage.tsx` | "Motion" signal only — converged. |
| `pages/HomePage.tsx` | DoD-2/3 already gated by convergence test. Drift signals are "Raw CSS vars + Motion" — defer; needs hero rewrite (Phase 3). |
| `pages/LoginPage.tsx` | Pre-auth, isolated. Defer. |
| `pages/NotFoundPage.tsx` | Trivial. "Motion" only. |
| `pages/PackDetailPage.tsx` | Already DoD-1/DoD-2 gated. "ErrorBanner" only. |
| `pages/SettingsEnvironmentsPage.tsx` | Read-only deployment view. Minor signals. Defer. |
| `pages/SettingsHealthPage.tsx` | Minor signals. Defer. |
| `pages/SettingsHubPage.tsx` | Hub overview, low drift surface. |
| `pages/settings/InputSafetySettings.tsx` | "ErrorBanner" only. |
| `pages/settings/OutputSafetySettings.tsx` | "Raw inputs" + "ErrorBanner" — could be folded into a future settings sweep but not in step-4 list. |
| `pages/WorkflowsPage.tsx` | "Raw inputs" — needs Phase 3 hero rewrite (Workflow Studio). Defer. |
| `pages/WorkflowStudioPage.tsx` | Studio canvas — Phase 3 rewrite scope. |
| `pages/AuditLogPage.tsx` (finish P1) | "Convert table shell to DataTable/Pagination" — wait until DataTable Phase 2 stabilizes. Defer to follow-up. |

## Per-page audit-doc updates (step 7)

After all 4 batches DONE, update `dashboard/docs/design-system-audit.md`:
- Move converged pages from P3/P4 priority to a "Converged in v2.5 drift sweep" line in the priority section.
- Mark RunDetailPage's raw-controls/raw-vars carve-out alongside its existing DoD-3 carve-out (line 13–14).
- Update the audit summary counters (lines 39–45): "Pages still containing raw `<input>/<select>/<textarea>` markup" should drop by N; "Pages still carrying raw CSS-var styling" should drop by N.

## Progress tracker

Mark batches as they ship.

- [ ] Batch A — Govern detail surfaces (4 pages, 1 carve-out)
- [ ] Batch B — Settings drift (2 pages) + DLQPage deletion
- [ ] Batch C — Govern Replay/Simulator/Analytics + listings (8 pages)
- [ ] Batch D — Quarantine + ApprovalsPage finish-P1 (2 pages)
- [ ] Step 7 — `design-system-audit.md` updated; this checklist marked all-batches-complete.

## Open questions for architect

1. **Batch ordering**: Plan step 4 suggests A → B → C → D by customer impact. Confirm OK with that order, or prefer another? (Yaron may want Batch D early for ApprovalsPage polish, since it's a high-traffic surface.)
2. **Out-of-scope pages**: 14 P3/P4 pages have minimal-signal drift (table above). Are they truly out of scope for v2.5 (defer to v3.x), or should a "Batch E" pick up the easy wins?
3. **AuditLogPage finish-P1**: deferred above pending DataTable stabilization. Confirm the dependency is real, or land it now in Batch C?
