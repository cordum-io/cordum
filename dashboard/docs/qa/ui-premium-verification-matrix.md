# UI Premium Overhaul — Verification Matrix

**Task:** task-84989aa0 — QA: Dashboard UI/UX Premium Overhaul Verification
**Epic:** epic-2e0ed1ee — Docs + Marketing GTM Polish (pre-Visa)
**QA worker:** worker-5896
**Date:** 2026-04-24
**Scope:** Three sweeps — Soft UI Evolution (mem-6dae4b5b), Level 2 polish, Level 3 Full Sweep (mem-60822b45).

## Legend

- ✅ PASS — meets DoD item with evidence
- ❌ FAIL — blocker; DoD item fails on this surface
- ⚠ PARTIAL — DoD item met but with caveats
- ⊖ OUT-OF-SCOPE — page not part of the premium sweep, graded at baseline
- — — not yet measured in this phase

## DoD Items

- **DoD-1** — "Premium Soft Control Surface" aesthetic (glassmorphism, backdrop blur, organic corners — `rounded-xl`/`rounded-2xl`, `--shadow-soft`)
- **DoD-2** — Smooth framer-motion transitions + staggered animations on HomePage, Policy Studio, Settings
- **DoD-3** — 12-column Bento Grid on Detail pages: Agent, Job, Run, Bundle
- **DoD-4** — WCAG AA contrast on new glass panels + softened palette
- **DoD-5** — Mobile responsiveness for all new grid layouts

## Primary surfaces matrix (full sweep targets)

| Page / Component | File | DoD-1 glass/soft | DoD-2 motion | DoD-3 bento-12 | DoD-4 a11y | DoD-5 mobile | Status |
|------------------|------|------------------|--------------|----------------|------------|--------------|--------|
| HomePage (Command Center) | `src/pages/HomePage.tsx` | — | ✅ motion (19 hits) | ✅ grid-cols-12 present | ⚠ (to axe) | — | in-scope |
| AgentDetailPage | `src/pages/AgentDetailPage.tsx` | — | — | ✅ grid-cols-12 present | — | — | in-scope |
| JobDetailPage | `src/pages/JobDetailPage.tsx` | — | — | ✅ grid-cols-12 present | — | — | in-scope |
| RunDetailPage | `src/pages/RunDetailPage.tsx` | — | ✅ motion (7 hits) | ❌ **grid-cols-12 MISSING** | — | — | **DoD-3 FAIL** confirmed |
| BundleDetailPage | `src/pages/govern/BundleDetailPage.tsx` | — | ❌ **0 motion hits** | ❌ **grid-cols-12 MISSING** | — | — | **DoD-2 + DoD-3 FAIL** confirmed |
| PolicyOverviewPage (Policy Studio) | `src/pages/govern/PolicyOverviewPage.tsx` | — | — | n/a | — | — | in-scope |
| BundlesPage | `src/pages/govern/BundlesPage.tsx` | — | — | n/a | — | — | in-scope |
| InputRulesPage | `src/pages/govern/InputRulesPage.tsx` | — | — | n/a | — | — | in-scope |
| OutputRulesPage | `src/pages/govern/OutputRulesPage.tsx` | — | — | n/a | — | — | in-scope |
| VelocityRulesPage | `src/pages/govern/VelocityRulesPage.tsx` | — | — | n/a | — | — | in-scope |
| SimulatorPage | `src/pages/govern/SimulatorPage.tsx` | — | — | n/a | — | — | in-scope |
| ReplayPage | `src/pages/govern/ReplayPage.tsx` | — | — | n/a | — | — | in-scope |
| PolicyAnalyticsPage | `src/pages/govern/PolicyAnalyticsPage.tsx` | — | — | n/a | — | — | in-scope |
| QuarantinePage | `src/pages/govern/QuarantinePage.tsx` | — | — | n/a | — | — | in-scope |
| TenantsPage | `src/pages/govern/TenantsPage.tsx` | — | — | n/a | — | — | in-scope |
| TenantDetailPage | `src/pages/govern/TenantDetailPage.tsx` | — | — | n/a | — | — | in-scope |

## Core data table pages (Level 3 row-stagger claim)

| Page | File | DoD-1 | DoD-2 staggered rows | DoD-4 | DoD-5 | Status |
|------|------|-------|----------------------|-------|-------|--------|
| JobsPage | `src/pages/JobsPage.tsx` | — | ❌ **container-only fade, no row stagger** | — | — | **DoD-2 FAIL** |
| AuditLogPage | `src/pages/AuditLogPage.tsx` | — | ❌ **container-only fade, no row stagger** | — | — | **DoD-2 FAIL** |
| AgentsPage | `src/pages/AgentsPage.tsx` | — | ❌ **container-only fade, no row stagger** | — | — | **DoD-2 FAIL** |
| ApprovalsPage | `src/pages/ApprovalsPage.tsx` | — | ⚠ per-card motion.article, no explicit stagger | — | — | DoD-2 PARTIAL |
| ApprovalDetailPage | `src/pages/approvals/ApprovalDetailPage.tsx` | — | — | — | — | in-scope |

## Settings surface (Level 3 instrument-card claim)

| Page | File | DoD-1 instrument-card | DoD-2 | DoD-4 | DoD-5 | Status |
|------|------|----------------------|-------|-------|-------|--------|
| SettingsHubPage | `src/pages/SettingsHubPage.tsx` | — | — | — | — | in-scope |
| SettingsConfigPage | `src/pages/SettingsConfigPage.tsx` | — | — | — | — | in-scope |
| SettingsKeysPage | `src/pages/SettingsKeysPage.tsx` | — | — | — | — | in-scope |
| SettingsMcpPage | `src/pages/SettingsMcpPage.tsx` | — | — | — | — | in-scope |
| SettingsNotificationsPage | `src/pages/SettingsNotificationsPage.tsx` | — | — | — | — | in-scope |
| SettingsEnvironmentsPage | `src/pages/SettingsEnvironmentsPage.tsx` | — | — | — | — | in-scope |
| SettingsUsersPage | `src/pages/SettingsUsersPage.tsx` | — | — | — | — | in-scope |
| SettingsHealthPage | `src/pages/SettingsHealthPage.tsx` | — | — | — | — | in-scope |
| SettingsSSOPage | `src/pages/settings/SettingsSSOPage.tsx` | — | — | — | — | in-scope |
| SettingsSCIMPage | `src/pages/settings/SettingsSCIMPage.tsx` | — | — | — | — | in-scope |
| SettingsAuditExportPage | `src/pages/settings/SettingsAuditExportPage.tsx` | — | — | — | — | in-scope |
| InputSafetySettings | `src/pages/settings/InputSafetySettings.tsx` | — | — | — | — | in-scope |
| OutputSafetySettings | `src/pages/settings/OutputSafetySettings.tsx` | — | — | — | — | in-scope |
| LicensePage | `src/pages/settings/LicensePage.tsx` | — | — | — | — | in-scope |

## Supporting pages touched incidentally by the sweep (framer-motion adopters)

| Page | File | DoD-1 | DoD-2 | DoD-4 | DoD-5 | Status |
|------|------|-------|-------|-------|-------|--------|
| JobsPage already above |  |  |  |  |  |  |
| AgentIdentityDetailPage | `src/pages/AgentIdentityDetailPage.tsx` | — | — | — | — | in-scope |
| SchemaDetailPage | `src/pages/SchemaDetailPage.tsx` | — | — | — | — | in-scope |
| SchemasPage | `src/pages/SchemasPage.tsx` | — | — | — | — | in-scope |
| PacksPage | `src/pages/PacksPage.tsx` | — | — | — | — | in-scope |
| PackDetailPage | `src/pages/PackDetailPage.tsx` | — | — | — | — | in-scope |
| TopicsPage | `src/pages/TopicsPage.tsx` | — | — | — | — | in-scope |
| DLQPage | `src/pages/DLQPage.tsx` | — | — | — | — | in-scope |
| WorkflowsPage | `src/pages/WorkflowsPage.tsx` | — | — | — | — | in-scope |
| WorkflowStudioPage | `src/pages/WorkflowStudioPage.tsx` | — | — | — | — | in-scope |
| MCPPage | `src/pages/MCPPage.tsx` | — | — | — | — | in-scope |
| DelegationsPage | `src/pages/DelegationsPage.tsx` | — | — | — | — | in-scope |
| EvalsPage | `src/pages/EvalsPage.tsx` | — | — | — | — | in-scope |
| EvalDatasetDetailPage | `src/pages/EvalDatasetDetailPage.tsx` | — | — | — | — | in-scope |
| EvalRunDetailPage | `src/pages/EvalRunDetailPage.tsx` | — | — | — | — | in-scope |
| LoginPage | `src/pages/LoginPage.tsx` | — | — | — | — | in-scope (public) |
| NotFoundPage | `src/pages/NotFoundPage.tsx` | — | — | — | — | in-scope (public) |

## Pages not yet touched by the sweep — baseline, out-of-scope

None identified in src/pages/ — framer-motion coverage grep showed 38 files; all route pages adopt the design system.

## Viewport matrix (applied per row above)

- **Desktop light** 1440×900 — reference
- **Desktop dark** 1440×900 — parity check
- **Tablet portrait** 768×1024
- **Tablet landscape** 1024×1366
- **Mobile** 375×812 (iPhone 12/13 mini)

## Machine-verifiable DoD gates — DesignSystemConvergence.test.ts

Run: `npx vitest run src/pages/DesignSystemConvergence.test.ts`

Result: **Tests 4 failed | 8 passed (12)**. Failures are intentional rejection evidence.

| Test case | DoD | Result | Reason |
|-----------|-----|--------|--------|
| HomePage renders motion primitives | DoD-2 | ✅ PASS | 19 `motion.` hits + framer-motion import |
| AgentDetailPage uses 12-col Bento Grid | DoD-3 | ✅ PASS | grid-cols-12 present |
| JobDetailPage uses 12-col Bento Grid | DoD-3 | ✅ PASS | grid-cols-12 present |
| **RunDetailPage uses 12-col Bento Grid** | DoD-3 | ❌ **FAIL** | grid-cols-12 absent (evidence for blocker) |
| **BundleDetailPage uses 12-col Bento Grid** | DoD-3 | ❌ **FAIL** | grid-cols-12 absent (evidence for blocker) |
| **BundleDetailPage adopts framer-motion** | DoD-2 | ❌ **FAIL** | no `from "framer-motion"` import, zero motion primitives (evidence for blocker) |
| AppShell applies glass-sidebar + glass-header | DoD-1 | ✅ PASS | both utilities applied |
| Settings hub uses instrument-card | DoD-1 | ✅ PASS | |
| Design tokens shadow-soft/radius/duration-soft exist for light + dark | DoD-1 | ✅ PASS | both `:root` and `.dark` blocks define the tokens |
| **Core data tables stagger rows (Jobs / Audit / Agents)** | DoD-2 | ❌ **FAIL** | JobsPage, AuditLogPage container-only fade; no per-row motion (evidence for blocker) |

## Evidence index

- Primitives greps: `docs/qa/evidence/greps.md`
- Screenshots desktop: `docs/qa/evidence/screens/`
- Screenshots mobile: `docs/qa/evidence/screens-mobile/`
- Performance traces: `docs/qa/evidence/perf/`
- axe-core violations: `docs/qa/evidence/a11y/`
- Regression gate: `docs/qa/evidence/gate.md`
- Final report: `docs/qa/ui-premium-overhaul-report-2026-04-24.md`

## Known risks surfaced during planning (to confirm during audit)

1. **BundleDetailPage** — grep: zero `grid-cols-12`, zero `motion.`. Likely DoD-3 fail + DoD-2 fail on that surface.
2. **RunDetailPage** — grep: `motion.` present, zero `grid-cols-12`. Likely DoD-3 fail.
3. **HomePage** — 19 `motion.` hits but no aria-* on primary module. Needs axe-core confirmation for DoD-4.
4. `useReducedMotion` consumer coverage limited to 9 files (vs. 38 motion-adopter pages). Delta is ungated motion — DoD-4 a11y risk.
5. Only 3 files contain `grid-cols-12` — plan says 4 detail pages + HomePage must all use it. Confirmed 2-of-4 detail pages miss the layout.
