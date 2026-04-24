# Dashboard Verification Rail — Draft

This is the draft text of a proposed Moe GLOBAL rail, authored under task-347388c0.
The rail codifies the verification commands that architects and workers MUST run
(and QA MUST enforce) before `moe.complete_task` on any task that touches files
under `cordum/dashboard/`.

## Why this rail

Two consecutive dashboard sweeps (`task-bd7eb4af` Soft UI Evolution and
`task-2bb626ec` Level 3 Full Sweep) shipped to REVIEW with **only
Docker-build-success** as their evidence. Both broke tests that a single
`npx vitest run` would have caught:

- `JobDetailPage.test.ts` — AnimatePresence added without updating framer-motion mock.
- `ChainIntegrityWidget.test.tsx` — `sm:grid-cols-3` → `lg:grid-cols-3` without test update.
- `PolicyOverviewPage.test.tsx` — widget mount removed, dead imports left behind.

See memories `mem-2ed5ee1a` (Level 3 reject) and `mem-7c390fb1` (Soft UI reject).

The `verification-before-completion` skill already says "no complete-claim without
fresh verification evidence", but it's generic. The dashboard loophole was
closing via "Docker-build passed" — a successful Docker image build does not run
the vitest suite. This rail nails the EXACT command set for dashboard work so
architects can't accidentally skip vitest, and QA has an auditable gate.

## Proposed rail text (GLOBAL scope, two rails)

Because a single rail line was too long (~490 chars for the command rail + ~330
for the rejection rail = ~820 combined), the proposal is **TWO GLOBAL rails**,
each <500 chars, addressing distinct concerns (WHAT-to-run vs QA-rejection-format).

### Rail 1 — DASHBOARD VERIFICATION RAIL (what to run)

```
DASHBOARD VERIFICATION RAIL: Tasks whose DoD or implementationPlan touches files under `cordum/dashboard/` MUST, before `moe.complete_task`, run these from `cordum/dashboard/` and paste each summary line into the final `complete_step` note: (1) `node ./node_modules/typescript/bin/tsc --noEmit`; (2) `npx vitest run`; (3) `npm run build`. tsc and vitest must not regress vs branch-point baseline. Docker-build-success is NOT a substitute. See skill `verification-before-completion`.
```

### Rail 2 — DASHBOARD QA REJECTION FORMAT

```
DASHBOARD QA REJECTION FORMAT: QA MUST `moe.qa_reject` any task touching `cordum/dashboard/` whose final `complete_step` note lacks tsc+vitest+build evidence per the DASHBOARD VERIFICATION RAIL, OR whose tsc-error count or vitest failed-count exceeds the branch-point baseline. `rejectionDetails` MUST cite the first failing gate, and for vitest the first new failing test as `<describe> > <it> (<path>:<line>)`.
```

## Command-choice rationale

- **tsc**: `node ./node_modules/typescript/bin/tsc --noEmit` per `dashboard/CLAUDE.md`
  which explicitly flags `npx tsc` as WRONG on this Windows/MSYS platform.
- **vitest**: `npx vitest run` (no targeted glob) per package.json `test` script.
  Full-suite is required — Level 3 broke tests in files the sweep didn't touch,
  so a targeted run would have missed them.
- **build**: `npm run build` (which runs `tsc -b && vite build`). Re-runs tsc in
  `-b` mode + bundles; catches config-only drift not caught by `--noEmit`. Phase 2
  verification shows `tsc -b` here is near-no-op (no project references in tsconfig),
  so `tsc --noEmit` is the real type-check gate.
- **Lint**: OMITTED from this rail proposal. Phase 2 verification showed HEAD
  already has 8 lint errors (4 of which are stale `eslint-disable-next-line
  <rulename>` directives pointing at rules not loaded by `eslint.config.mjs`).
  Adding lint as a rail gate would force every dashboard task to clean up
  pre-existing baseline debt — outside scope. Filed as a follow-up: once the
  dashboard eslint baseline is green (zero errors, zero warnings), amend this
  rail via `MODIFY_RAIL` to add `npm run lint` as gate (4).

## "Must not regress vs baseline" framing

Phase 2 showed HEAD is **already** red on tsc (2 errors) and vitest (11 failed
/ 1624 total). Phrasing the rail as "exit 0" would force every dashboard task
to first resolve unrelated parallel-worker breakage — that's scope creep. The
phrasing **"must not regress vs branch-point baseline"** is operationally
honest: a task must capture the pre-change baseline and demonstrate its own
diff introduced zero new failures. QA does the arithmetic in the rejection
rail.

## Scope predicate

The predicate `Tasks whose DoD or implementationPlan touches files under cordum/dashboard/`
lives in the rail text itself because Moe doesn't have a file-scoped rail
primitive — architects self-apply the predicate at plan time.

This mirrors the pattern already used by `feedback_qa_integration_tag.md`
(scopes to scheduler/audit/gateway/store) and `feedback_frontend_design_skill.md`
(scopes to dashboard).

## Character counts

- Rail 1: ~490 chars
- Rail 2: ~330 chars

Both well under any practical rail-line limit. If the daemon does enforce a hard
~500-char cap, Rail 1 is still within it.

## Not covered (deferred follow-ups)

- `npm ci` as a prerequisite — skipped because it adds ~30s of churn to every
  dashboard task, and stale `node_modules` is a rare failure mode worth catching
  at the specific task boundary where tsconfig or lockfile changed, not on every
  task.
- A CI-level gate (blocking merge on red dashboard tests) exists separately as
  `Dashboard Tests` required check; this rail is the pre-PR gate for Moe workers.
