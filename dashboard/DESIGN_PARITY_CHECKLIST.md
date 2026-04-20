# Design Parity Checklist

Tracks visual parity between the Cordum dashboard implementation and the "Control Surface" design language spec (`cordum-dashboard-design-language.md` v0.6.0). Each route is evaluated against five dimensions: density/spacing, token fidelity, status semantics, motion, and accessibility.

## Parity Validation Workflow

Run these commands from `dashboard/` before marking any route as passing:

```bash
cd dashboard
node ./node_modules/typescript/bin/tsc --noEmit
npx vitest run
npm run build
```

All three must exit cleanly. TypeScript catches token misuse, tests verify component behavior, and the production build ensures no dead-code or bundle issues.

---

## Route-by-Route Evidence Matrix

### AppShell (`/` — layout wrapper)

| Dimension | Status | Notes |
|-----------|--------|-------|
| Density / Spacing | PASS | 48px sidebar item rhythm, 4px grid alignment, compact header |
| Token Fidelity | PASS | `surface-glass` on sidebar/header, CSS variable consumption verified |
| Status Semantics | PASS | Active nav uses `accent` token, badge counts on Approvals/Failures |
| Motion | PASS | Sidebar collapse transition 200ms ease-out, no layout shift |
| Accessibility | PASS | `aria-current="page"` on active link, keyboard nav through sidebar items |

### Security Overview (`/` — HomePage)

| Dimension | Status | Notes |
|-----------|--------|-------|
| Density / Spacing | PASS | Metric cards on 4px grid, instrument card layout with 2px accent top border |
| Token Fidelity | PASS | `surface1` card backgrounds, `ink` primary text, `muted` secondary labels |
| Status Semantics | PASS | Danger/warning/success tokens for threat indicators, correct color mapping |
| Motion | PASS | Count-up animation on metric values, staggered card entrance |
| Accessibility | PASS | Screen reader labels on metric cards, color not sole status indicator |

### Approvals (`/approvals`)

| Dimension | Status | Notes |
|-----------|--------|-------|
| Density / Spacing | PASS | 48px table row rhythm, compact action buttons, proper cell padding |
| Token Fidelity | PASS | `surface1` table rows, `surface2` hover state, `accent` approve button |
| Status Semantics | PASS | Pending (warning), approved (success), denied (danger) badge colors |
| Motion | PASS | Row highlight on hover 150ms, approval action feedback animation |
| Accessibility | PASS | Table headers associated with cells, action buttons labeled by job context |

### Jobs (`/jobs`)

| Dimension | Status | Notes |
|-----------|--------|-------|
| Density / Spacing | PASS | Dense table layout, 48px rows, filter bar compact alignment |
| Token Fidelity | PASS | Status badges use semantic tokens, `surface1`/`surface2` layering correct |
| Status Semantics | PASS | Full state machine coverage (PENDING through SUCCEEDED/FAILED/TIMEOUT) |
| Motion | PASS | Real-time status transitions via WS, smooth badge color changes |
| Accessibility | PASS | Sortable columns announced, filter controls labeled, status conveyed by text+color |

### Runs (`/runs`)

| Dimension | Status | Notes |
|-----------|--------|-------|
| Density / Spacing | PASS | Workflow run cards on 4px grid, step timeline compact layout |
| Token Fidelity | PASS | `surface1` run cards, `surface2` step details, correct token inheritance |
| Status Semantics | PASS | Run/step states mapped to semantic colors consistently with Jobs page |
| Motion | PASS | Step progress animation, expandable detail transition 200ms |
| Accessibility | PASS | Run status announced on focus, timeline navigable via keyboard |

---

## Intentional Deviations

| Area | Spec Expectation | Implementation | Rationale |
|------|-----------------|----------------|-----------|
| Sidebar width | 240px fixed | 240px with 64px collapsed state | Added collapse for small viewports; density improves on narrow screens |
| Table pagination | Infinite scroll | Page-based with size selector | Bounded DOM size preferred for large job/run datasets (10k+ rows) |
| Font loading | System fallback chain | FOUT with swap strategy | Plus Jakarta Sans/Inter loaded via `font-display: swap` to avoid invisible text |

---

## Final Gate Results

**Date:** 2026-02-25

| Check | Result |
|-------|--------|
| TypeScript (`tsc --noEmit`) | PASS |
| Test suite (`vitest run`) | PASS |
| Production build (`npm run build`) | PASS |
| Visual parity review (all 5 routes) | PASS |
| Accessibility audit (axe-core) | PASS |

All gates passing. Dashboard implementation is at parity with the Control Surface design language spec.
