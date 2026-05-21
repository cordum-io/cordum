# Pre-Submit DoD Checklist

This is the checklist every Cordum worker must satisfy **before** calling
`moe.complete_task` on a code-touching task. It is the worker-side companion
to the `PRE-SUBMIT DOD CHECKLIST` and `DASHBOARD VERIFICATION RAIL` project
rails. QA enforces these requirements at review time.

## Why this exists

Two CI gaps caused a string of regressions to ship to REVIEW:

1. `golangci-lint` in the Lint workflow runs per touched package, so a
   broken import or missing symbol in a sibling package was never surfaced
   until someone ran `go build ./...` by hand.
2. Dashboard tasks landed with only "Docker build succeeded" evidence, even
   when `tsc --noEmit` and `npx vitest run` were red.

The fix is **two layers of enforcement**: a hard CI gate (`.github/workflows/ci.yml`
runs `go build ./...` and `go vet ./...` from the repo root before the
per-package lint pass) and this checklist, which forces workers to paste the
matching evidence into their handoff note so QA can verify without re-running
the gates themselves.

## What you must paste into the final `complete_step` / `complete_task` note

For **every code-touching Cordum task**, paste the following from the **repo
root** (`cordum/`), not from a subpackage:

```
$ go build ./...
<concise output, empty on success>
EXIT: 0

$ go vet ./...
<concise output, empty on success>
EXIT: 0
```

- Both commands must exit `0`. A non-zero exit blocks completion.
- Regression rule: if the task established a branch-point baseline that
  already has non-zero build or vet failures (because of unrelated peer
  activity on the shared branch), the post-change run must not introduce
  any *new* failures versus that baseline. Cite the baseline output in the
  note when you invoke this exception.
- Run the commands against the actual tree you are about to push. Do not
  paste old output from earlier in the session — re-run after your final
  edit.
- If a peer worker's uncommitted local modifications make your local run
  red but tracked `HEAD` is green, prove `HEAD`-cleanliness via
  `git archive HEAD | tar -x -C <tmp>` and re-run the gates inside that
  archive directory, then cite both runs.

### Cross-platform note

The two Go commands are identical on Windows PowerShell, macOS, and Linux.
Always run from the repo root using repo-root-relative paths
(`go build ./...`, not `cd cmd/foo && go build`). Do not silence failures
caused by stale local files — investigate the root cause and either commit
the missing implementation, delete the stale package, or `moe.report_blocked`
if the fix is outside your task scope.

## Additional gates for dashboard tasks

Any task whose `definitionOfDone` or `implementationPlan` touches files under
`cordum/dashboard/` **must additionally** paste the three dashboard gates
(from `cordum/dashboard/`):

```
$ node ./node_modules/typescript/bin/tsc --noEmit
<concise output>
EXIT: 0

$ npx vitest run
<summary line: "Test Files <n> passed | Tests <n> passed">
EXIT: 0

$ npm run build
<summary line: "built in <n> ms" + asset count>
EXIT: 0
```

The dashboard gates are required **in addition to** root-level `go build ./...`
and `go vet ./...`, not as a substitute. `npm run build` succeeding is **not**
a substitute for `tsc --noEmit` or `vitest run`. See the
`DASHBOARD VERIFICATION RAIL` and `DASHBOARD QA REJECTION FORMAT` project
rails for the QA-side enforcement contract (rejection details must cite the
first failing gate, and for vitest the first failing test as
`<describe> > <it> (<path>:<line>)`).

## Per-DoD-item evidence map

Before calling `moe.complete_task`, prepare an evidence map that pastes **one
line per DoD item** in the task object, citing the change or verification
output that satisfies it. The architect can amend the DoD via
`moe.add_comment` plus an edited task description **before** you submit;
chat-only acks do not count. QA may reject any task whose final
`complete_step` note lacks the per-DoD-item evidence map.

## Branch policy

All work currently lands on the active consolidation branch
`wip/2026-05-15-orphan-rescue` (PR #276). No per-task feature branches and
no new PRs. Commit on the shared branch with pathspec-restricted
`git commit -- <my-files>` to avoid absorbing peer WIP, then `git pull
--rebase` and push.

## When this checklist does not apply

Documentation-only tasks (no Go or TypeScript source files modified) are
exempt from the Go gates but still require the per-DoD-item evidence map.
Dashboard-only doc edits under `cordum/dashboard/` are exempt from the
dashboard tsc/vitest/build gates unless they change a `.ts` / `.tsx` / `.css`
file or `package.json`.
