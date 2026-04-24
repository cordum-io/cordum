# Dashboard Verification Rail — Command Verification

Evidence that the commands proposed by the DASHBOARD VERIFICATION RAIL
(task-347388c0) are correct and that the rail as phrased is necessary.

All runs performed from `D:\Cordum\cordum\dashboard\` on HEAD at task-start
time (2026-04-25 ~00:15 local). Workspace is active — multiple parallel
worker/architect sessions are modifying files; timestamps captured per run.

## Gate 1 — `node ./node_modules/typescript/bin/tsc --noEmit`

**Snapshot A (~00:16 local):**

```
src/pages/CopilotSessionPage.tsx(89,9): error TS2322: Type 'Element' is not assignable to type 'string'.
src/pages/CopilotSessionPage.tsx(124,33): error TS2322: Type '{ rows: number; cols: number; }' is not assignable to type 'IntrinsicAttributes & { rows?: number | undefined; }'.
  Property 'cols' does not exist on type 'IntrinsicAttributes & { rows?: number | undefined; }'.
TSC_EXIT=2
```

**Snapshot B (~00:20 local, re-run after parallel worker pushed fix):**

```
TSC_EXIT=0
```

**Finding:** tsc command works. Between snapshots A and B, another worker
resolved the CopilotSessionPage.tsx errors (work on task-b18e / parallel
dashboard work). That volatility is exactly why the rail says
"must not regress vs branch-point baseline" rather than "exit 0" — the
baseline may already be non-zero errors due to parallel worker activity.

## Gate 2 — `npm run lint` (DROPPED FROM RAIL)

**Snapshot A:**

```
src/api/transform.ts
  1184:1  error  Definition for rule '@typescript-eslint/no-explicit-any' was not found
  1199:1  error  Definition for rule '@typescript-eslint/no-explicit-any' was not found
src/components/delegations/DelegationChainViz.tsx
  120:15  error  Elements with the ARIA role "treeitem" must have the following attributes defined: aria-selected
src/components/policy/PolicySimulator.tsx
  553:5  error  Definition for rule 'react-hooks/exhaustive-deps' was not found
src/components/ui/Tabs.tsx
  41:9  error  The attribute aria-pressed is not supported by the role tab
src/hooks/useAudit.ts
  138:5  error  Definition for rule 'react-hooks/exhaustive-deps' was not found
src/hooks/useUrlFilters.ts
  117:5  error  Definition for rule 'react-hooks/exhaustive-deps' was not found
  135:5  error  Definition for rule 'react-hooks/exhaustive-deps' was not found
src/lib/logger.ts  (3 warnings — unused eslint-disable directives)

✖ 11 problems (8 errors, 3 warnings)
LINT_EXIT=1
```

**Finding:** HEAD already has 8 lint errors and 3 warnings. 5 of the 8 errors
are stale `eslint-disable-next-line <rulename>` directives pointing at rules
that `dashboard/eslint.config.mjs` does not load (config only includes
`jsx-a11y`, not `@typescript-eslint` or `react-hooks`). These are pre-existing
baseline debt, not regressions from recent work.

Including `npm run lint` in the rail would force every dashboard task to
clean up this baseline before shipping. That's out of scope for the rail's
purpose (catch test regressions). **Decision:** lint is dropped from this
proposal. File as a follow-up once dashboard's eslint baseline is green.

## Gate 3 — `npx vitest run`

**Snapshot A (~00:17 local):** `Tests  11 failed | 1613 passed (1624)` —
7 test files failed, 1 unhandled rejection.

**Snapshot B (~00:21 local, after parallel landings):**

```
 FAIL  src/components/ChainIntegrityWidget.test.tsx > primary metric row stacks 1-col on mobile and goes to 3-col at sm+
 FAIL  src/pages/DesignSystemConvergence.test.ts > DoD-3 — RunDetailPage uses 12-column Bento Grid
 FAIL  src/pages/DesignSystemConvergence.test.ts > DoD-3 — BundleDetailPage uses 12-column Bento Grid
 FAIL  src/pages/DesignSystemConvergence.test.ts > DoD-2 — BundleDetailPage adopts framer-motion
 FAIL  src/pages/DesignSystemConvergence.test.ts > DoD-2 — core data tables stagger rows (Level 3 claim)
 FAIL  src/pages/JobDetailPage.test.ts > renders the governance tab and lazy-mounts the timeline on activation
 FAIL  src/pages/govern/PolicyOverviewPage.test.tsx > mounts the ChainIntegrityWidget inside the Overview tab content

Tests  7 failed | 1617 passed (1624)
VITEST_EXIT=1
```

**Finding:** HEAD carries 7 failing tests at baseline snapshot B. Composition:

- **3 known Level 3 regressions** (expected per mem-2ed5ee1a):
  - `ChainIntegrityWidget.test.tsx > primary metric row stacks 1-col on mobile…`
  - `JobDetailPage.test.ts > renders the governance tab and lazy-mounts the timeline…`
  - `PolicyOverviewPage.test.tsx > mounts the ChainIntegrityWidget…`
  - Already tracked by qa-a7f4 tasks: task-651bf160, task-e56f3d7c, task-14d012e6.
- **4 intentional DoD-gate tests** (DesignSystemConvergence.test.ts) added by
  QA as enforcement for Level 3 unmet DoD items (bento grids + stagger). These
  will turn green as follow-up tasks land (task-c154ff08, task-900ada1f,
  task-b461cebe) per mem-edb2a26f.

**These 7 failing tests are EXACTLY what the DASHBOARD VERIFICATION RAIL
is meant to protect.** Without the rail, an architect could complete a
dashboard task with `Docker-build passed` as the sole evidence, and
introduce an 8th, 9th, 10th failure hidden in a Docker-success green light.

## Gate 4 — `npm run build`

**Snapshot A (~00:17 local):**

```
dist/assets/… (38 asset lines emitted)
✓ built in 615ms
BUILD_EXIT=0
```

**Finding — THE LOOPHOLE IN ACTION:** build exit 0 despite tsc --noEmit
exit 2 AND 11 vitest failures. Vite's dev-oriented build pipeline does not
re-run the full type check (tsconfig has `noEmit: true` so `tsc -b` is
largely a no-op for type checking with no project references). This is
precisely the gap the rail closes: the ONLY evidence Soft UI Evolution and
Level 3 Full Sweep provided before REVIEW was `Docker build succeeded` —
which is the CI-image equivalent of `npm run build` passing.

## Summary

| Gate | Command | Exit code (snapshot B) | Kept in rail? |
|------|---------|------------------------|---------------|
| tsc | `node ./node_modules/typescript/bin/tsc --noEmit` | 0 | ✅ |
| lint | `npm run lint` | 1 (8 errors baseline) | ❌ dropped — baseline cleanup needed first |
| vitest | `npx vitest run` | 1 (7 failed / 1624) | ✅ |
| build | `npm run build` | 0 | ✅ |

The rail text in `rail-vitest-green-draft.md` was updated after this phase:

1. Dropped lint from the gate list.
2. Changed the enforcement phrasing from "exit 0" to "must not regress vs
   branch-point baseline" because HEAD already carries non-zero baseline in
   tsc and vitest, and parallel worker activity makes the baseline volatile.
3. Added the baseline-volatility note to the QA rejection rail so QA rejects
   NEW regressions, not pre-existing baseline.

<!-- Phase 4 evidence (rail visibility) appended below -->

## PENDING APPROVAL — rails filed, awaiting human review

**Filed at 2026-04-25 local (~00:23):**

| Proposal | Scope | Status | Char count |
|----------|-------|--------|------------|
| `prop-8cc95268` — DASHBOARD VERIFICATION RAIL | GLOBAL / ADD_RAIL | PENDING | 482 |
| `prop-5a162a16` — DASHBOARD QA REJECTION FORMAT | GLOBAL / ADD_RAIL | PENDING | 405 |

**moe.get_context visibility check** (same timestamp):

```json
"allRails": {
  "global": [],
  "epic": [ ...4 epic-2e0ed1ee rails... ],
  "task": []
}
```

Both rails are NOT yet visible in `allRails.global`. `moe.propose_rail` returns
`status: "PENDING"` with message `"Proposal submitted for human review."`
regardless of `project.settings.approvalMode=TURBO` — rail changes are a
special class that always requires human approval because they affect every
task globally. This matches the plan Phase 9 risk note: _"If approval is
human-gated, worker files but flags waiting status."_

**`moe.check_approval` reports `approved: true` for task-347388c0** but that
refers to the TASK PLAN approval (which was TURBO auto-approved back at
2026-04-24T20:45:41Z), not to either rail proposal. check_approval does not
query rail-proposal status.

### DoD item 3 impact

DoD item 3 (`The rail is visible to architects when they call moe.get_context
on a dashboard task`) cannot be satisfied until a human approves both
proposals. Options considered:

1. Poll `check_approval` indefinitely — wrong tool, doesn't apply to rail proposals.
2. `moe.report_blocked` — formally blocks the task until human review. Accurate but
   conservative; the rest of the DoD (items 1, 2) and plan steps 5-7 (skill
   cross-link, adversarial review, PR + commit + chat announce) can proceed in
   parallel. Blocking now stalls that work.
3. Proceed through remaining steps documenting PENDING state, complete_task
   with explicit flag in summary that rails are awaiting approval, rely on
   QA to verify visibility before `qa_approve`. CHOSEN.

**Post-approval verification protocol** (to be executed by QA or human
reviewer): after approval, call `moe.get_context({ taskId: "task-347388c0" })`
and confirm both rail texts appear in `allRails.global`. Append the VERIFIED
VISIBLE block below (empty for now).

### VERIFIED VISIBLE (append on approval)

<!-- _Awaiting human approval of prop-8cc95268 and prop-5a162a16. When both are
approved, re-run moe.get_context and paste the allRails.global contents here._ -->

