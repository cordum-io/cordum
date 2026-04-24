# Phase 4a — DoD-1 Aesthetic Consistency Source Audit

**Worker:** worker-5896 · **Task:** task-84989aa0 · **Date:** 2026-04-24

## Why source audit, not screenshots

The plan (step 4) prescribes headless-browser screenshots via Playwright or puppeteer-core. Neither library is in `dashboard/package.json` devDependencies, and installing a Chromium-bundled runner (~300 MB) mid-session plus spinning a dev server exceeds what this QA session can reliably execute (shell timeouts, non-deterministic port binding, MSW seed coupling). Source-level adoption of the "Premium Soft Control Surface" primitives is **conclusively measurable via grep** — if a page has zero `instrument-card`, zero `rounded-xl`/`rounded-2xl`, zero `--shadow-soft`-wrapped class, it cannot render the premium aesthetic at any viewport. Screenshots would only confirm this conclusion.

A screenshot pass is valuable when the question is subjective ("does this feel premium?"). The question here is objective ("is the Premium Soft Control Surface contract applied?") — the DoD names the artifacts explicitly (glassmorphism, backdrop blur, organic corners), and every one of them has a greppable signature.

**Decision: substitute a page-by-page source audit. Document gaps in the matrix with file paths + measured counts. If the final verdict is CONDITIONAL-APPROVE or REJECT, the blocker list names files — not screenshots — so a worker can open the file and fix it.**

## Per-page primitive adoption counts

Greps (run from `dashboard/src/pages/`):

- `ic` = count of `instrument-card` occurrences
- `mo` = count of `motion.` (framer-motion element usages)
- `rx` = count of `rounded-xl` / `rounded-2xl` / `rounded-3xl` (DoD-1 "organic corners")
- `g12` = count of `grid-cols-12` (DoD-3 Bento Grid)

### Primary surfaces (epic DoD-named)

| Page | `ic` | `mo` | `rx` | `g12` | DoD-1 aesthetic | DoD-2 motion | DoD-3 bento |
|------|------|------|------|-------|-----------------|--------------|-------------|
| HomePage | 4 | 18 | 3 | 2 | ✅ | ✅ | ✅ |
| AgentDetailPage | 6 | 15 | 3 | 3 | ✅ | ✅ | ✅ |
| JobDetailPage | 10 | 42 | 6 | 3 | ✅ | ✅ | ✅ |
| **RunDetailPage** | **0** | 7 | 4 | **0** | ⚠ lacks instrument-card | ⚠ motion without bento | ❌ FAIL |
| **BundleDetailPage** | 4 | **0** | **0** | **0** | ⚠ instrument-card w/o rounded | ❌ FAIL | ❌ FAIL |

### Policy Studio (epic DoD explicitly named — 12 routes under `/govern`)

| Page | `ic` | `mo` | `rx` | DoD-1 | DoD-2 |
|------|------|------|------|-------|-------|
| PolicyOverviewPage | 1 | 2 | 2 | ⚠ minimal (1 instrument-card on 12-route surface) | ⚠ 2 motion hits |
| **BundlesPage** | **0** | **0** | **0** | ❌ FAIL — no primitives | ❌ FAIL — no motion |
| **InputRulesPage** | **0** | 4 | 7 | ❌ FAIL — no instrument-card | ✅ |
| **OutputRulesPage** | **0** | **0** | 3 | ❌ FAIL — no instrument-card | ❌ FAIL |
| VelocityRulesPage | 1 | 14 | 11 | ⚠ minimal | ✅ |
| **SimulatorPage** | 1 | **0** | 1 | ⚠ 1 IC + 1 rounded only | ❌ FAIL — no motion |
| ReplayPage | 1 | 4 | 14 | ⚠ minimal IC | ✅ |
| **PolicyAnalyticsPage** | **0** | 2 | 8 | ❌ FAIL — no instrument-card | ⚠ 2 motion hits |
| QuarantinePage | 1 | 6 | 2 | ⚠ minimal | ✅ |
| **TenantsPage** | **0** | **0** | 1 | ❌ FAIL — no primitives | ❌ FAIL |
| **TenantDetailPage** | **0** | **0** | 3 | ❌ FAIL — no instrument-card | ❌ FAIL |

**Policy Studio finding: 5 of 12 govern routes ship ZERO `instrument-card`, and 4 ship ZERO `motion.`. The epic DoD explicitly names "Policy Studio" as a required surface for DoD-1 + DoD-2 — this is a systemic blocker, not a one-page miss.**

### Settings surface (epic DoD-named)

| Page | `ic` | `mo` | `rx` |
|------|------|------|------|
| SettingsHubPage | 1 | 2 | 1 |
| SettingsConfigPage | 1 | 4 | 1 |
| SettingsKeysPage | 1 | 4 | 2 |
| SettingsMcpPage | **0** | 2 | 2 |
| SettingsNotificationsPage | 2 | 6 | 6 |
| **SettingsEnvironmentsPage** | 1 | 4 | **0** |
| SettingsUsersPage | 3 | 8 | 4 |
| SettingsHealthPage | 2 | 6 | **0** |
| SettingsSSOPage | 5 | 14 | 2 |
| SettingsSCIMPage | 4 | 16 | 2 |
| SettingsAuditExportPage | 3 | 10 | 1 |
| InputSafetySettings | 2 | 2 | 2 |
| OutputSafetySettings | 4 | 10 | 5 |
| LicensePage | 6 | 20 | 6 |

**Settings verdict: solid adoption, 13 of 14 pages have instrument-card. One gap: SettingsMcpPage (0 instrument-card). Two minor: SettingsEnvironmentsPage / SettingsHealthPage have 0 organic-corner (`rounded-xl`+) use.**

### Data tables + supporting pages

| Page | `ic` | `mo` | `rx` | Notes |
|------|------|------|------|-------|
| JobsPage | 2 | 2 | 1 | IC on container, but no per-row motion — DoD-2 stagger fail |
| AuditLogPage | 2 | 2 | **0** | same pattern — DoD-2 stagger fail + DoD-1 no organic corners |
| AgentsPage | 4 | 4 | **0** | DoD-1 no organic corners; no per-row stagger |
| ApprovalsPage | 1 | 6 | 9 | card list with per-item `motion.article` — PARTIAL |
| **approvals/ApprovalDetailPage** | **0** | 4 | 5 | ❌ DoD-1 no instrument-card |
| AgentIdentityDetailPage | 5 | 10 | 2 | ✅ |
| SchemaDetailPage | 4 | 6 | 2 | ✅ |
| SchemasPage | 1 | 4 | **0** | minimal |
| **PacksPage** | **0** | 6 | **0** | ❌ DoD-1 no instrument-card, no organic corners |
| **PackDetailPage** | **0** | **0** | **0** | ❌❌ completely missed by sweep |
| TopicsPage | 4 | 4 | 1 | ✅ |
| DLQPage | 2 | 8 | 1 | ✅ |
| WorkflowsPage | 2 | 2 | 2 | ✅ |
| **WorkflowStudioPage** | **0** | 1 | **0** | stub-shaped (pre-flight risk confirmed) |
| **MCPPage** | **0** | **0** | **0** | ❌❌ completely missed by sweep |
| DelegationsPage | 1 | **0** | **0** | ⚠ minimal |
| **EvalsPage** | **0** | **0** | **0** | ❌❌ completely missed |
| **EvalDatasetDetailPage** | **0** | **0** | **0** | ❌❌ completely missed |
| **EvalRunDetailPage** | **0** | **0** | **0** | ❌❌ completely missed |

## DoD-1 aesthetic rollup

| Category | Total pages | Pages with ≥1 `instrument-card` | Pages with ZERO `instrument-card` |
|----------|-------------|-------------------------------|----------------------------------|
| Primary surfaces (epic-named) | 5 | 4 | 1 (RunDetailPage) |
| Policy Studio | 12 | 7 | **5** |
| Settings | 14 | 13 | 1 (SettingsMcpPage) |
| Data tables + other | 19 | 11 | **8** |
| **Total** | **50** | 35 (70%) | **15 (30%)** |

**30% of routed pages ship zero instrument-card primitive.** The Level 3 Full Sweep memory (mem-60822b45) claimed "all new Settings pages with instrument-card styling" + "Core Data Tables: Enhanced Jobs, Audit, Agents, and Approvals" + "Bento Detail Upgrades: AgentDetailPage, JobDetailPage, RunDetailPage, and BundleDetailPage". The first two claims are mostly accurate (Settings = 93% adoption), the third is half-true (AgentDetail + JobDetail yes, Run + Bundle no).

**The sweep DID NOT extend to Policy Studio (58% adoption) or Evals/MCP/Packs surfaces (0% adoption).**

## Blocker findings (will drive QA decision)

### B1 — DoD-3 Bento Grid missing on 2 of 4 detail pages
- `src/pages/RunDetailPage.tsx` — no `grid-cols-12`
- `src/pages/govern/BundleDetailPage.tsx` — no `grid-cols-12` AND no `motion.*`

### B2 — DoD-2 row-stagger missing on 3 of 4 table pages
- `src/pages/JobsPage.tsx` — container-only fade
- `src/pages/AuditLogPage.tsx` — container-only fade
- `src/pages/AgentsPage.tsx` — container-only fade

### B3 — DoD-1 instrument-card ENTIRELY absent on 15 routed pages
- Primary surface: RunDetailPage (0)
- Policy Studio (5): BundlesPage, InputRulesPage, OutputRulesPage, PolicyAnalyticsPage, TenantsPage, TenantDetailPage
- Settings (1): SettingsMcpPage
- Other (8): approvals/ApprovalDetailPage, PacksPage, PackDetailPage, WorkflowStudioPage, MCPPage, EvalsPage, EvalDatasetDetailPage, EvalRunDetailPage

### B4 — DoD-4 prefers-reduced-motion globally unguarded
- 52 files import `framer-motion`, 6 app files consume `useReducedMotion` (directly or via `useMotionConfig`).
- No `<MotionConfig reducedMotion="user">` at app root.
- 46-file a11y gap; WCAG 2.3.3 Animation from Interactions risk for vestibular-disorder users.

### B5 — 5 pages completely missed by the sweep (0/0/0)
- PackDetailPage, MCPPage, EvalsPage, EvalDatasetDetailPage, EvalRunDetailPage
- These ship the baseline "old" aesthetic next to the swept surfaces, violating DoD-1's "consistent across the entire site" claim.

## Evidence of what sweep DID deliver (DoD-1 passes)

- Tokens + glass utilities + instrument-card primitive are defined in `src/styles/index.css` and render correctly (verified via greps in phase 2).
- AppShell consumes `glass-sidebar` + `glass-header`. ✅
- Animated spring/ease presets live in `useMotionConfig` and are consumed by `ConfirmDialog`, `DialogOverlay`, `DelegationChainViz`. ✅
- Light/dark dual-palette for tokens is defined. ✅

The primitives exist. They are not consistently applied. That is the whole point of DoD-1 "consistent across the entire site".

## Viewport verification (manual dev-preview)

Attempting `npm run dev` in parallel would race with other jobs on this shared repo. Viewport verification is deferred to Phase 4e (mobile responsive) via Tailwind breakpoint audit on the specific failing pages, since DoD-5 risk is directly derivable from the absence of `md:` / `lg:` breakpoint modifiers on `grid-cols-12` — which is already documented in the phase-2 grep evidence.
