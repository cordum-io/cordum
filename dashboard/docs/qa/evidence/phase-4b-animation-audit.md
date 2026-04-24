# Phase 4b — DoD-2 Animation Audit

**Worker:** worker-5896 · **Task:** task-84989aa0 · **Date:** 2026-04-24

## Methodology substitution

Plan prescribes Chrome DevTools Performance panel or Playwright tracing on cold loads of HomePage, PolicyOverviewPage, SettingsHubPage. No Playwright in devDeps (see phase-4a note). Substituting with **source-level animation contract audit**:

- (i) staggered list render — greppable via `staggerChildren` / per-index `delay: i * `
- (ii) janky frames — derivable from which CSS properties are animated (GPU-accelerated `transform`/`opacity` vs layout-forcing `width`/`height`/`top`)
- (iii) prefers-reduced-motion — greppable via `useReducedMotion()` / `MotionConfig reducedMotion`

## HomePage — epic-DoD-named

```
318:        initial="hidden"
319:        animate="visible"
320:        variants={{
321:          visible: { transition: { staggerChildren: 0.05 } },
322:        }}
330:            <motion.div variants={{ hidden: { opacity: 0, y: 10 }, visible: { opacity: 1, y: 0 } }}>
349:            <motion.div variants={{ hidden: { opacity: 0, y: 10 }, visible: { opacity: 1, y: 0 } }}>
377:            <motion.div variants={{ hidden: { opacity: 0, y: 10 }, visible: { opacity: 1, y: 0 } }}>
401:            <motion.div variants={{ hidden: { opacity: 0, y: 10 }, visible: { opacity: 1, y: 0 } }}>
... and more container-level fade/slide at lines 457, 571, 624, 716 with delay offsets 0.1, 0.15, 0.2, 0.25
```

- (i) Staggered list render: **✅ PASS** — real `staggerChildren: 0.05` driving 4 KPI children.
- (ii) Jank risk: **✅ LOW** — all animations use `opacity` + `y` (translateY). GPU-accelerated. No layout thrash.
- (iii) prefers-reduced-motion: **❌ FAIL** — HomePage does NOT consume `useReducedMotion()`. The entire entry sequence plays regardless of OS preference. WCAG 2.3.3 gap.

## PolicyOverviewPage — epic-DoD-named "Policy Studio"

```
228:        initial={{ opacity: 0, y: 12 }}
229:        animate={{ opacity: 1, y: 0 }}
230:        transition={{ duration: 0.3, delay: 0.05 }}
```

- (i) Staggered list render: **❌ FAIL** — ONE container-level fade. No `staggerChildren`, no per-index delay, no per-row motion. Represents the entire "Policy Studio" surface for this DoD — and the sweep did not ship a staggered render on the flagship governance page.
- (ii) Jank risk: LOW — opacity + y only.
- (iii) prefers-reduced-motion: **❌ FAIL** — no guard.

## SettingsHubPage — epic-DoD-named

```
72:            <motion.button
73:              key={card.path}
74:              initial={{ opacity: 0, y: 12 }}
75:              animate={{ opacity: 1, y: 0 }}
76:              transition={{ delay: i * 0.04, duration: 0.3 }}
```

- (i) Staggered list render: **✅ PASS** — per-card index-based stagger (`delay: i * 0.04`). Effectively equivalent to `staggerChildren`.
- (ii) Jank risk: LOW — opacity + y only.
- (iii) prefers-reduced-motion: **❌ FAIL** — no guard.

## prefers-reduced-motion — system-wide gap (DoD-4 a11y blocker)

52 files import `framer-motion`. 6 files consume reduced-motion awareness:

| File | Via |
|------|-----|
| src/components/layout/AppShell.tsx | `useReducedMotion()` direct |
| src/components/governance/DecisionNode.tsx | `useReducedMotion()` direct |
| src/hooks/useMotionConfig.ts | defines the helper |
| src/components/ui/ConfirmDialog.tsx | `useMotionConfig()` |
| src/components/ui/DialogOverlay.tsx | `useMotionConfig()` |
| src/components/delegations/DelegationChainViz.tsx | `useMotionConfig()` |

**46-file delta.** All the route pages named in the epic DoD (HomePage, Policy Studio, Settings pages, detail pages) animate without honoring `prefers-reduced-motion: reduce`.

Framer-motion provides two correct remediations — neither is in place:

1. **Global wrapper** — `<MotionConfig reducedMotion="user">` at app root (`src/App.tsx` or `src/main.tsx`). Single-line fix; `MotionConfig` is already imported by `framer-motion`. `grep "<MotionConfig" src/` returns zero.
2. **Per-page guard** — each page pulls `useReducedMotion()` and conditionally substitutes `initial={false}` or `transition={{ duration: 0 }}`. More work, but more granular.

**This is a DoD-4 rejection-grade blocker.** WCAG 2.3.3 Animation from Interactions requires motion can be disabled — the dashboard does not provide that control for the swept surfaces. Users with vestibular disorders will see fade/slide transitions regardless of OS preference.

## DoD-2 rollup

| Page | Stagger | Jank | Reduced-motion | Verdict |
|------|---------|------|----------------|---------|
| HomePage | ✅ | ✅ | ❌ | PARTIAL (motion present but ungated) |
| PolicyOverviewPage | ❌ | ✅ | ❌ | FAIL (no stagger + ungated) |
| SettingsHubPage | ✅ | ✅ | ❌ | PARTIAL |

**DoD-2 compound verdict: FAIL** on (i) Policy Studio lacks stagger and (iii) reduced-motion entirely unguarded globally. These are the same issues surfaced by the phase-2 grep gate — now with per-page code confirmation.

## Data table stagger — reprise from Phase 2

JobsPage / AuditLogPage / AgentsPage have container-level `motion.div` with initial/animate but no stagger and no per-row motion. Pre-existing QA rejection (msg-5d55467f) flagged this; no commit has remediated it since. **Unchanged: FAIL.**
