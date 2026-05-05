# Edge Hook Runner Sandboxing

EDGE-068 hardens the local Claude hook execution boundary. The boundary spans
the `cordum-claude` wrapper and the Edge Claude launcher that spawns `git`,
`cordum-agentd`, and `claude`.

## Audit summary

Prior exec-site audit found 10 production subprocess call sites. Four are in the
hook runner boundary and all already used argv-mode instead of shell strings:

| Site | Spawn | Control |
|---|---|---|
| `cmd/cordum-claude/main.go` | `cordumctl edge claude ...` | Wrapped with `safeexec.CommandContext`. |
| `core/edge/claude/launcher_helpers.go` | `git -C <cwd> ...` | Wrapped with `safeexec.RunCapture` and bounded stdout/stderr. |
| `core/edge/claude/launcher_process.go` | `cordum-agentd` | Wrapped with `safeexec.CommandContext`. |
| `core/edge/claude/launcher_process.go` | `claude --settings <tmp> ...` | Wrapped with `safeexec.CommandContext` plus path-argv prefix checks. |

`cmd/cordumctl doctor` still has deliberate shell execution for operator
diagnostics. That path is outside the hook runner boundary and should remain
documented as an exception if a lint guard covers shell invocations.

## Controls

### Argv, not shell

Hook payload fields never form a shell command string. The launcher constructs
subprocesses as `argv0` plus an argument slice. Shell metacharacters such as
`;`, `$()`, and backticks remain literal argv bytes.

### Environment scrub

`safeexec` builds a new child environment from an allowlist:

- exact safe process keys such as `PATH`, `HOME`, `USERPROFILE`, `TEMP`, `TMP`,
  `TMPDIR`, `LANG`, `TZ`, and Windows process basics in non-production mode;
- prefixes `CORDUM_` and `LC_`;
- explicit non-production allowlist entries for narrow tests/dev flows.

Dangerous injection vectors are denied even if otherwise allowed:
`LD_PRELOAD`, `DYLD_*`, `NODE_OPTIONS`, `BASH_ENV`, and variables beginning
with `_`. NUL bytes in env keys or values are rejected before spawning.

### Path normalization

Path-shaped executables are cleaned, made absolute, and optionally checked
against allowed prefixes before `exec.Cmd` is returned. `..` traversal is
rejected during parse/normalization, not after opening a file. The command
working directory is normalized the same way. Launcher temp roots, generated
settings paths, state dirs, and path-bearing Claude argv values are also
validated before process start.

### IO bounds

Captured subprocess output goes through `safeexec.RunCapture` with explicit
stdout/stderr byte caps. Oversized captured stdin/stdout/stderr returns a
structured `safeexec` limit error instead of growing memory without bound.
Interactive Claude stdout/stderr are streamed to caller-provided writers and
are not accumulated in memory.

### Dev cannot weaken production

Development may opt into extra env keys with `CORDUM_DEV_ALLOW_ENV`. Production
sets `CORDUM_HOOK_PROD_LOCK=1`, which ignores that development override, strips
`PATH`, and uses the fixed allowlist. This prevents local/dev settings from
weakening enterprise managed hook defaults or smuggling a malicious first-match
binary path into children.

## Residual assumptions

- `PATH` is preserved only outside production lock because Cordum runs on
  developer laptops and must launch standard tools. Production-locked launches
  should prefer absolute paths for managed binaries.
- OS-level sandboxing such as seccomp/AppArmor is out of v1 scope.
- Symlink hardening is limited to normalization and optional prefix checks; the
  configured managed settings directory remains an OS permission boundary.

## Regression coverage

`core/edge/safeexec` tests cover literal shell metacharacters, env scrub,
explicit allowlisting, path normalization, traversal rejection, context
cancellation, NUL rejection, IO caps, dev-vs-prod env precedence, and prefix
checks. The companion issue-draft addendum lives at
`docs/internal/issue-drafts/security/SEC-012-edge-hook-runner-sandboxing.md`.
