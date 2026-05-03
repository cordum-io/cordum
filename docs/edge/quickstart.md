# Cordum Edge — quickstart

Three paths to a governed Claude Code session, ordered by setup cost. The
30-second path is the demo-day workflow; the 5-minute path is the durable
local-dev posture; the deep dive links to the full reference.

## 30-second path — env vars only

```bash
export CORDUM_GATEWAY=https://localhost:8081
export CORDUM_API_KEY=<your-key>
export CORDUM_TENANT_ID=default
export CORDUM_PRINCIPAL_ID=<you>

cordumctl edge claude
```

`cordumctl edge claude --print-config` dumps the resolved configuration
(api_key redacted) before launch so you can sanity-check what the wrapper
will use. This is enough for any developer with `cordumctl`, `cordum-hook`,
`cordum-agentd`, and `claude` on PATH.

## 5-minute path — scaffolded config + `cordum-claude` shortcut

```bash
cordumctl edge init --principal <you> --non-interactive
#   wrote ./cordum.yaml
#   wrote ./cordum-claude.sh (or .ps1 on Windows)

export CORDUM_API_KEY=<your-key>     # only the secret stays in env
./cordum-claude.sh                   # zero-flag invocation
```

The scaffold writes a checked-in-friendly `./cordum.yaml` with every config
field populated *except* the API key, which is written as
`${CORDUM_API_KEY}` — an env-reference, never a plaintext value. The
generated `cordum-claude.sh` (or `.ps1`) is a one-liner wrapper that
delegates to `cordumctl edge claude` so the team gets a short, memorable
command name without learning the full cordumctl tree. Re-running
`cordumctl edge init` is idempotent; use `--force` to refresh after editing
flags.

A standalone `cordum-claude` binary is also produced by `make build` and
behaves identically — `cordum-claude --print-config` is `cordumctl edge
claude --print-config` with one fewer level of typing.

## Full reference

For every flag, every env var, the precedence order, and the YAML schema,
see [cordumctl edge claude](cordumctl-edge-claude.md). For local recovery
flow, see the [Edge runbook](runbook.md). For the wider P0 architecture,
see the [Edge README](README.md).

## Cross-references

- Full launch wrapper contract: [cordumctl-edge-claude.md](cordumctl-edge-claude.md)
- Local diagnostics: [cordumctl-edge-doctor.md](cordumctl-edge-doctor.md)
- Hook contract: [cordum-hook.md](cordum-hook.md)
- Agentd lifecycle: [cordum-agentd.md](cordum-agentd.md)
- Configuration env vars: [configuration.md](configuration.md)
- Operator runbook: [runbook.md](runbook.md)
- 30-minute walkthrough (broader Edge story, not just the wrapper):
  [../quickstart-edge.md](../quickstart-edge.md)
