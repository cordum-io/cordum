# SEC-012 — Edge hook runner sandboxing addendum

## Boundary

The EDGE-068 execution boundary covers the local Claude wrapper and launcher:
`cmd/cordum-claude`, `core/edge/claude`, and the shared
`core/edge/safeexec` subprocess chokepoint. The boundary spawns `git`,
`cordum-agentd`, and `claude`; it does not include operator diagnostics such as
`cordumctl doctor`.

## Threats

- Shell injection from untrusted Claude hook payload fields or wrapper args.
- Environment smuggling through dynamic loader, shell startup, or Node runtime
  variables.
- Path traversal in temporary managed settings, agentd state, executable, or
  path-bearing argv values.
- Unbounded subprocess stdin/stdout/stderr reads growing hook memory.
- Developer-local settings weakening enterprise managed defaults.

## Controls

1. All in-boundary subprocesses use argv slices through `safeexec`; shell
   interpreters such as `sh -c`, `cmd /C`, and PowerShell `-Command` are linted.
2. Child environments are rebuilt from a whitelist plus explicit dev-only
   additions; `LD_PRELOAD`, `DYLD_*`, `NODE_OPTIONS`, `BASH_ENV`, and `_*` are
   always denied.
3. Executables, working dirs, temp settings dirs, state dirs, and path-like
   argv values are cleaned, absolutized, and rejected on `..` traversal before
   file creation or process start.
4. Captured subprocess output uses `safeexec.RunCapture` byte caps. Interactive
   Claude streams are not buffered in memory; the generated `--settings` path
   and caller path args are prefix-validated.
5. `CORDUM_HOOK_PROD_LOCK=1` ignores `CORDUM_DEV_ALLOW_ENV` and strips `PATH`
   so development overrides cannot weaken production/managed execution.

## Regression hooks

Focused tests cover shell metacharacter literals, env scrub, prod-lock
precedence, IO caps, traversal rejection, state-dir parse rejection,
path-bearing argv prefix checks, and structured hook error envelopes.
