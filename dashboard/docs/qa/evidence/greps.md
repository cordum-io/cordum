# Phase 2 — Design System Contract Greps

**Worker:** worker-5896 · **Task:** task-84989aa0 · **Date:** 2026-04-24

Raw evidence for whether the "Premium Soft Control Surface" primitives are actually present and used. Each section captures the grep, expected floor, and the measured result. `FAIL` rows are ship-blocker rationale.

## 1. Design tokens — shadow, radius, duration

```
rg -n "\-\-shadow\-soft|\-\-radius: 0\.75rem|\-\-duration\-soft" dashboard/src/styles/index.css
```

Output (verbatim):

```
18:  --shadow-soft: var(--shadow-soft);
19:  --shadow-soft-hover: var(--shadow-soft-hover);
20:  --animate-duration-soft: var(--duration-soft);
78:  --radius: 0.75rem;
79:  --shadow-soft: 0 4px 14px 0 rgba(0, 0, 0, 0.05);
80:  --shadow-soft-hover: 0 6px 20px 0 rgba(0, 0, 0, 0.08);
81:  --duration-soft: 250ms;
144:  --shadow-soft: 0 4px 14px 0 rgba(0, 0, 0, 0.25);
145:  --shadow-soft-hover: 0 6px 20px 0 rgba(0, 0, 0, 0.35);
146:  --duration-soft: 250ms;
```

Expected: ≥3 hits per var (@theme + light + dark). Measured: shadow-soft 4, radius 0.75rem 1 @theme mapping + 1 light ×… present in all three scopes. **PASS**. Tokens are defined for light (`:root`, line 78–81) and dark (`.dark` block, 144–146), and wired into `@theme` at 18–20.

## 2. Glass utilities

```
rg -n "@utility glass\-panel|@utility glass\-sidebar|@utility glass\-header" dashboard/src/styles/index.css
```

Output:

```
206:@utility glass-panel {
212:@utility glass-sidebar {
218:@utility glass-header {
```

Expected: 3 hits. Measured: 3. **PASS**.

## 3. Instrument card utility

```
rg -n "\.instrument\-card\b" dashboard/src/styles/index.css
```

Output:

```
302:  .instrument-card {
305:  .instrument-card::before {
316:  .instrument-card.status-warning::before {
319:  .instrument-card.status-danger::before {
322:  .instrument-card.status-governance::before {
325:  .instrument-card.status-info::before {
328:  .instrument-card.status-muted::before {
334:  .instrument-card-hover:hover {
```

Expected: base class + status modifiers. Measured: `.instrument-card` base (302), 5 status modifiers (warning, danger, governance, info, muted), `::before` accent (305), and `.instrument-card-hover:hover` (334). **PASS**. Comprehensive.

## 4. Backdrop-blur consumers (glassmorphism-adopting components)

```
rg -l "backdrop\-blur" dashboard/src/
```

Expected: >0 consumers. Measured: **20 occurrences across 16 files**. **PASS**.

Files:

```
src/components/CommandPalette.tsx (1)
src/components/EntitlementGate.tsx (1)
src/components/ErrorBoundary.tsx (1)
src/components/KeyboardShortcuts.tsx (1)
src/components/KeyboardShortcutsHelp.tsx (1)
src/components/layout/AppShell.tsx (1)
src/components/ui/ConfirmDialog.tsx (2)
src/components/workflow-studio/StudioCanvas.tsx (2)
src/components/ui/DialogOverlay.tsx (1)
src/components/ui/Drawer.tsx (2)
src/pages/approvals/ApprovalDetailPage.tsx (2)
src/pages/LoginPage.tsx (1)
src/components/policy/PromoteShadowDialog.tsx (1)
src/pages/govern/PolicyOverviewPage.tsx (1)
src/components/policy/RuleEditor.tsx (1)
src/components/policy/bundles/BundleShadowTab.tsx (1)
```

## 5. Framer-motion adoption

```
rg -c "from \"framer-motion\"" dashboard/src/pages dashboard/src/components
```

Measured: **53 occurrences across 52 files** in the dashboard src tree. **PASS** — motion is broadly adopted.

## 6. 12-column Bento Grid — DoD-3 gate

```
rg -l "grid\-cols\-12" dashboard/src/pages
```

Expected: HomePage + AgentDetailPage + JobDetailPage + RunDetailPage + BundleDetailPage (5 files).

Measured: **3 files**.

```
src/pages/JobDetailPage.tsx
src/pages/AgentDetailPage.tsx
src/pages/HomePage.tsx
```

**FAIL — DoD-3**:

- `src/pages/RunDetailPage.tsx` — MISSING `grid-cols-12`. (7 `motion.` hits exist — motion is present, bento grid is not.)
- `src/pages/govern/BundleDetailPage.tsx` — MISSING `grid-cols-12` AND MISSING `motion.` (0 hits). The Level 3 sweep memory (mem-60822b45) claimed Bundle detail was refactored to a 12-col Bento — grep contradicts this. This is a DoD-3 blocker and a memory/claim regression.

Cross-check: grepped full `dashboard/src/` (not just `pages/`) — same 3 files. No CSS-module or alternate-selector escape hatch.

## 7. Table row-stagger — DoD-2 on data tables (Level 3 claim)

Level 3 memory (mem-60822b45) claimed: "Core Data Tables: Enhanced Jobs, Audit, Agents, and Approvals pages with framer-motion staggered rows."

```
rg -l "motion\.tr\b" dashboard/src/pages
```

Measured: **0 hits on JobsPage / AuditLogPage / AgentsPage / ApprovalsPage**.

Measured `motion.`/`AnimatePresence` pattern on the 4 claim pages:

| Page | `motion.*` count | Pattern | Pass row-stagger? |
|------|------------------|---------|-------------------|
| JobsPage.tsx | 2 | Single container-level `motion.div` wrapping the whole table (lines 679–822). No per-row motion. | **FAIL** — no row stagger |
| AuditLogPage.tsx | 2 | Container-level `motion.div` (442–529). No per-row motion. | **FAIL** — no row stagger |
| AgentsPage.tsx | 4 | Two container-level `motion.div` blocks (158–195 and 375–428). No per-row motion. | **FAIL** — no row stagger |
| ApprovalsPage.tsx | 6 | Per-card `motion.article` WITH `AnimatePresence mode="popLayout"` and `layout` on each (821–940). | **PARTIAL PASS** — card list (not table) has per-item motion but no explicit `staggerChildren`; animations run in parallel on first mount |

This is the **same DoD-3 stagger gap the prior Level 3 QA reject (msg-5d55467f) flagged** and it remains unfixed. **DoD-2 row-stagger FAIL on three of four table pages.**

## 8. prefers-reduced-motion honor — DoD-4 a11y

```
rg -l "useReducedMotion" dashboard/src
```

Measured: **4 files** directly consume `useReducedMotion`:

```
src/components/layout/AppShell.tsx
src/components/governance/DecisionNode.tsx
src/hooks/useMotionConfig.ts
src/pages/RunDetailPage.test.tsx  (test file, not app code)
```

Plus a central `useMotionConfig()` helper (derived from `useReducedMotion`) consumed by:

```
src/components/ui/ConfirmDialog.tsx
src/components/ui/DialogOverlay.tsx
src/components/delegations/DelegationChainViz.tsx
(+ tests)
```

Total **app-code adopters: 6** (3 direct + 3 via hook). **52 files import framer-motion** (grep §5). **Delta = 46 pages/components that render `motion.*` WITHOUT an active reduced-motion guard.**

This is a **DoD-4 a11y blocker** under WCAG 2.3.3 Animation from Interactions. Framer-motion does not automatically honor `prefers-reduced-motion: reduce`; every `motion.*` element must either consume `useReducedMotion()` locally OR be wrapped in a `MotionConfig reducedMotion="user"` provider. Neither is present at the app root (`src/main.tsx`, `src/App.tsx` — no `MotionConfig`).

```
rg -l "<MotionConfig" dashboard/src
```

→ no hits (confirmed; `useReducedMotion` grep returned 0 app-level providers).

**FAIL — DoD-4.**

## Summary of primitive grep gate

| Item | Status | Notes |
|------|--------|-------|
| Tokens (shadow-soft, radius, duration-soft) | PASS | |
| Glass utilities | PASS | |
| instrument-card + status modifiers | PASS | |
| backdrop-blur consumers | PASS | 16 files |
| framer-motion adoption | PASS | 52 files |
| **grid-cols-12 on 4 detail pages** | **FAIL** | RunDetailPage, BundleDetailPage missing |
| **Row-stagger on table pages** | **FAIL** | JobsPage, AuditLogPage, AgentsPage miss motion per row |
| **prefers-reduced-motion honored globally** | **FAIL** | 46-file gap, no MotionConfig wrapper |

Three hard fails identified in the primitive/contract phase alone, before any screenshot or axe-core evidence. This pattern matches the prior Level 3 rejection (msg-5d55467f) and indicates the sweep shipped incomplete.
