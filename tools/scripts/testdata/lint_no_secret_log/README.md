EDGE-068b — Fixture corpus for `lint_no_secret_log.sh` Phase 4 (argv-only exec guard).

These fixtures are exercised by `tools/scripts/lint_no_secret_log.test.sh`. They are
not compiled by the rest of the tree:

* Every fixture carries `//go:build ignore` so `go build ./...` skips them.
* The fixture root lives under `tools/scripts/testdata/`, which is outside the
  lint script's default Phase 4 find roots (`$REPO_ROOT/cmd`, `$REPO_ROOT/core`),
  so a regular CI lint pass never reaches them.
* The test harness sets `LINT_SCAN_ROOTS_OVERRIDE=<this-dir>` per case so the
  lint scans only the fixture in question.

Subdirectories
--------------

`phase4_pass/` — Go files the guard MUST NOT flag.
  - `argv_only.go`             pure-argv exec, no shell interpreter.
  - `argv_with_dash_c_flag.go` `go test -c` — `-c` is the go-test compile flag,
                               not a shell flag. Guard must require an
                               interpreter name before the `-c` check.

`phase4_fail/` — Go files the guard MUST flag.
  - `sh_dash_c.go`              `exec.Command("sh", "-c", payload)`.
  - `bin_sh_dash_c.go`          absolute Unix shell path `/bin/sh -c`.
  - `bash_dash_c.go`             `bash -c`.
  - `cmd_slash_c.go`            Windows `cmd /C`.
  - `cmd_exe_slash_c.go`        Windows `cmd.exe /c` (lowercase, exercises
                                case-insensitive flag match).
  - `powershell_dash_command.go` PowerShell `-Command` form.
  - `multiline.go`              shell call split across multiple source lines
                                — exercises the awk multi-line paren tracker.
  - `absolute_path_sh.go`       absolute path `/usr/bin/sh -c` — exercises the
                                `[^"]*[/\\]` path-prefix branch of the
                                interpreter regex.

`phase4_exception/` — Go files that look like shell exec patterns but carry the
documented `// no-shell-exec-lint: <reason>` marker and MUST pass.
  - `doctor_pattern.go`  mirrors `cmd/cordumctl/doctor.go:878-883` runtime.GOOS
                         branch with both Windows and Unix shell variants.
  - `marker_test.go`     inline marker without a runtime branch, demonstrating
                         the marker shape used elsewhere.

Adding a new fixture
--------------------

1. Drop the `.go` file in the relevant subdir. Use `//go:build ignore` so the
   regular Go build skips it.
2. For a `phase4_fail/` fixture, append a case to the harness so the harness
   asserts a `FAIL:` line mentioning the file name.
3. Keep fixtures minimal — one exec pattern per file. Larger files dilute the
   FAIL signal and slow the harness.
