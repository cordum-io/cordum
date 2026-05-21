# `no-shell-exec-lint` — argv-only exec convention

Production Go code in `cordum/cmd/` and `cordum/core/` must invoke subprocesses
with `exec.Command` / `exec.CommandContext` using the **argv form** — never via
a shell interpreter (`sh -c`, `bash -c`, `cmd /C`, `cmd.exe /c`,
`powershell -Command`, `pwsh -Command`).

`tools/scripts/lint_no_secret_log.sh` Phase 4 (EDGE-068) enforces this at CI
time. `tools/scripts/lint_no_secret_log.test.sh` (EDGE-068b) self-tests the
guard against a fixture corpus under
`tools/scripts/testdata/lint_no_secret_log/`.

## Why

Hook-boundary subprocesses (Edge command hooks, doctor repairs, agentd-driven
operations) receive operator- or LLM-supplied data. Routing that data through
a shell interpreter exposes the parent process to:

- **Command injection** — metacharacters in the payload (`;`, `|`, `` ` ``,
  `$()`, newlines) execute additional commands the caller did not author.
- **Quoting drift** — Windows `cmd /C` and POSIX `sh -c` quote differently;
  cross-platform code that builds a single command string ships subtle parser
  bugs.
- **Audit gap** — once the payload reaches a shell, the audit log only
  captures the *interpreter* invocation, not the effective syscalls.

Argv-form `exec.Command` passes each argument as a discrete C-string to
`execve(2)` / `CreateProcess`; the kernel/loader treats them as opaque tokens.
No interpreter, no parsing, no injection vector.

## What the lint flags

The guard fires when **all** of the following hold inside a single
`exec.Command` / `exec.CommandContext(...)` call (possibly spread across
multiple source lines):

1. The first argument is a quoted shell-interpreter token — `"sh"`,
   `"bash"`, `"cmd"`, `"cmd.exe"`, `"powershell"`, `"powershell.exe"`,
   `"pwsh"`, `"pwsh.exe"`, **including absolute paths** such as
   `"/bin/sh"`, `"/usr/bin/bash"`, or `"C:\\Windows\\System32\\cmd.exe"`.
2. A subsequent quoted argument is the shell command-string flag: `"-c"`,
   `"-C"`, `"/c"`, `"/C"`, or `"-Command"`.
3. The call site does not carry the `// no-shell-exec-lint` suppression
   marker on any of its source lines.

The flag check requires an interpreter name first, so unrelated `-c` usages
(e.g. `exec.Command("go", "test", "-c", ...)`, where `-c` is the go-test
compile flag) do **not** trip the guard.

## Suppressing a single call (the `// no-shell-exec-lint` marker)

Some call sites must route through the platform shell. The supported example is
`cmd/cordumctl/doctor.go::doctorShellRunner` — it executes operator-confirmed
repair commands that the operator has already typed verbatim, so the
interpreter is the contract, not a security boundary.

To suppress the guard at that exact call site, add the marker comment on (or
in) the call:

```go
cmd = exec.CommandContext(ctx, "/bin/sh", "-c", command) // no-shell-exec-lint: operator-confirmed doctor repair only
```

Required marker shape:

- The literal token `no-shell-exec-lint` must appear in the flattened call
  text (the awk in the lint script `gsub`s whitespace, so the marker may live
  on any source line of a multi-line call).
- Always include a brief **reason** after the marker (`// no-shell-exec-lint:
  <why this is safe>`). The lint does not enforce reason text mechanically,
  but unsourced markers are rejected at review.

Do not move the marker to package scope, a build tag, or a directive line —
the bypass is strictly per-call.

## Current exceptions

| File | Lines | Reason |
| --- | --- | --- |
| `cmd/cordumctl/doctor.go` | 880, 882 | `doctorShellRunner` executes operator-confirmed doctor-repair commands. The operator types the command verbatim before any execution. Windows uses `cmd /C`; POSIX uses `/bin/sh -c`. |

Adding a new exception requires:

1. An architect review on the originating Moe task documenting why argv form is
   not feasible.
2. The `// no-shell-exec-lint: <reason>` marker placed on the exact call.
3. A line appended to this table in the same commit that adds the marker.

## Self-tests

The harness `tools/scripts/lint_no_secret_log.test.sh` exercises:

- **Pass corpus** (`testdata/lint_no_secret_log/phase4_pass/`) — argv-only
  exec + the `go test -c` false-positive case.
- **Fail corpus** (`testdata/lint_no_secret_log/phase4_fail/`) — eight shell
  patterns including the multi-line case (`exec.CommandContext` split across
  source lines) and the absolute-path case (`/usr/bin/sh -c`).
- **Exception corpus** (`testdata/lint_no_secret_log/phase4_exception/`) —
  the doctor.go runtime.GOOS branch and a minimum-shape inline marker.
- **Default-tree invariant** — with `LINT_SCAN_ROOTS_OVERRIDE` unset, the
  lint must still pass on the real `cmd/` and `core/` trees. Protects
  against the fixture corpus accidentally bleeding into the production scan.

CI runs both `lint_no_secret_log.sh` and `lint_no_secret_log.test.sh`. A
regression that weakens the guard (e.g. drops a shell variant from the
interpreter regex) fails the harness before merging.

## Known limitations

- The marker bypass is text-based: a reviewer can wave through a marker that
  lacks a reason. The convention requires a reason; mechanical enforcement of
  the `<reason>` suffix would belong to a follow-up task.
- The lint's `find` roots are `$REPO_ROOT/cmd` and `$REPO_ROOT/core`. Code
  living elsewhere (e.g. ad-hoc scripts under `tools/`) is not scanned by
  this guard.
- The harness assumes POSIX shell + GNU/MSYS find. CI runs on
  `ubuntu-latest`; local Git Bash on Windows also works.
