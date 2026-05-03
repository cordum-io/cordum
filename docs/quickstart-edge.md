# Cordum Edge — Quickstart

This page walks a new engineer from a clean `git clone` to a working
governed Claude Code session in under 30 minutes. It is the **minimum
copy-paste path**; for the full reference, jump to
[`docs/edge/README.md`](edge/README.md).

The wrapper here is the developer/demo path, **not** enterprise enforcement.
Enterprise rollout requires managed Claude settings, signed binaries, and a
deployment-controlled keychain — see
[`docs/edge/README.md`](edge/README.md) "Enforcement layers".

---

## 1. What you are doing

You will:

1. Build and start the Cordum stack via Docker Compose: five core services
   (API Gateway, Scheduler, Safety Kernel, Workflow Engine, Context Engine),
   the dashboard, NATS, Redis, and two demo workers
   (`demo-quickstart-worker`, `mock-bank-worker`). The standalone MCP server
   (`cmd/cordum-mcp`) builds from source but is not part of `make dev-up`.
2. Run the fake-hook E2E to confirm the `cordum-hook -> cordum-agentd ->
   Gateway -> Safety Kernel` path is wired correctly. This is the same
   acceptance script CI uses; it produces five `PASS edge_*` lines.
3. Optionally launch a real Claude Code session through `cordumctl edge claude`
   and watch denials, approvals, and artifacts land in the dashboard.

If you want the architecture story before commands, read
[`docs/edge/README.md`](edge/README.md) first.

---

## 2. Prerequisites

| Tool | Version | Notes |
| --- | --- | --- |
| Docker | Compose v2 plugin | Docker Desktop on macOS/Windows; native engine on Linux. |
| GNU Make | any | Drives `make dev-up`/`make build`. macOS/Linux ship it; on Windows/MSYS install via `pacman -S make` or substitute the raw `docker compose ...` and `go build ...` commands shown below. |
| Go | 1.24+ | For local binary builds. |
| Node.js | 18+ | For dashboard build/test. |
| `openssl` | any recent | Used to mint a local API key. |
| `curl`, `jq`, `bash` | any | The fake-hook script needs these. On Windows/MSYS, the repo ships `tools/scripts/jq.exe` as a fallback. |
| Claude Code | optional | Only required for the *manual* real-Claude demo at the end. |

> Windows/MSYS users: use Git Bash or WSL. The fake-hook script and several
> targets assume POSIX shell semantics. PowerShell is not supported for the
> E2E script itself. If `make` is unavailable, every `make` line in this
> guide has a raw-command fallback in the same code block.

---

## 3. Five-minute happy path

```bash
# 1. Clone and enter the repo
git clone https://github.com/cordum-io/cordum.git
cd cordum

# 2. Seed local config and an API key. The Compose stack reads CORDUM_API_KEY
#    from .env at startup, so the same value must be in .env *and* exported in
#    your shell. Generate once:
[ -f .env ] || cp .env.example .env   # only seed if you don't already have one
export CORDUM_API_KEY=$(openssl rand -hex 32)
sed -i.bak "s|^CORDUM_API_KEY=.*|CORDUM_API_KEY=${CORDUM_API_KEY}|" .env
rm -f .env.bak

export CORDUM_TENANT_ID=default
# Gateway uses self-issued TLS in dev (cert minted into ./certs by the stack).
export CORDUM_GATEWAY=https://localhost:8081
export CORDUM_GATEWAY_TLS_CA="$(pwd)/certs/ca/ca.crt"  # consumed by cordum-agentd

# 3. Bring up the full stack (~2-3 minutes first time)
make dev-up
# Equivalent without make:
#   docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d --build

# 4. Wait for services to be healthy. The quickstart script polls for you:
./tools/scripts/quickstart.sh --skip-build --skip-smoke

# 5. Build the Edge binaries (cordum-hook, cordum-agentd, cordumctl)
make build SERVICE=cordum-hook
make build SERVICE=cordum-agentd
make build SERVICE=cordumctl
# Equivalent without make (Windows/MSYS or no GNU Make):
#   go build -o bin/cordum-hook   ./cmd/cordum-hook
#   go build -o bin/cordum-agentd ./cmd/cordum-agentd
#   go build -o bin/cordumctl     ./cmd/cordumctl
# (On Windows append .exe to each output path.)

# 6. Install the demo Edge policy pack so the deny / approval rules fire.
#    Without this, /api/v1/edge/evaluate runs against a default-allow policy
#    and the PreToolUse deny gate of the fake-hook E2E will not match.
#    The pack ships only policy overlays (no pool overlay), so it registers
#    as INACTIVE — that is expected; the policy fragments are still applied.
export CORDUM_TLS_CA="$(pwd)/certs/ca/ca.crt"
./bin/cordumctl pack install ./examples/cordum-edge-pack

# 7. Run the fake-hook E2E in strict mode
CORDUM_INTEGRATION=1 bash tools/scripts/edge_fake_hook_e2e.sh
```

You should see, in order:

```text
PASS edge_session_setup
PASS edge_pretooluse_deny
PASS edge_approval_flow
PASS edge_posttooluse_artifact
PASS edge_evidence_export
```

If you see those five PASS lines, the full Edge P0 path is working
end-to-end against your local stack.

> If the script prints `SKIP edge_fake_hook_e2e: ...` instead, you forgot to
> set `CORDUM_INTEGRATION=1`. The script intentionally skips when integration
> mode is unset so it does not flap CI runs that lack a stack.

---

## 4. Open the dashboard

```text
http://localhost:8082
```

> The dashboard image listens on container port `8080` and is published to
> host port `8082` (see `docker-compose.yml`). The `5173` port is only used
> when running the dashboard via `npm run dev` directly, which is not part
> of `make dev-up`.

Navigate to **Edge Sessions** (left nav). You should see one session row from
the fake-hook script — `complete` status, with PreToolUse deny, an
approval+retry, a PostToolUse artifact, and an evidence export link.

Click into the session to see the timeline and event inspector. The inspector
shows decisions, approval refs, and artifact pointer metadata. It deliberately
does not render raw payloads, raw prompts, or command output; that data does
not enter the dashboard cache.

---

## 5. Optional: run real Claude Code through Cordum

This step requires Claude Code installed and on `PATH`, or pointed at via
`--claude-path`. Skip it if you do not have Claude installed; the fake-hook
E2E above already proved the governance path.

```bash
# Generate a temp tenant principal and launch Claude through the wrapper
export CORDUM_PRINCIPAL_ID=demo-user
./bin/cordumctl edge claude
```

The wrapper will:

1. Generate a one-shot agentd nonce (kept in process env, never written to
   `~/.claude/settings.json`).
2. Spawn a local `cordum-agentd` against your local Gateway.
3. Render a temporary Claude command-hook settings file.
4. Launch Claude Code with that settings file.

Inside Claude, try:

| Prompt | Expected outcome |
| --- | --- |
| `read .env` | **Denied** before the tool runs. Claude sees the deny reason. |
| `edit README.md` (or another guarded path per the demo policy) | `REQUIRE_APPROVAL`. The dashboard shows a pending approval; approve there, then retry in Claude. The retry consumes the approval once. |
| Any safe action (`ls`, `grep` in a non-guarded path) | Allowed quietly. Cordum stays out of the way. |

Watch the dashboard Edge Session timeline update as you go. When done, exit
Claude (`Ctrl-D`) and the wrapper tears down agentd + the temp settings dir.

---

## 6. Cleanup

```bash
# Stop the stack (containers only; volumes preserved)
make dev-down
# Equivalent without make:
#   docker compose down

# Full reset (drops Redis + all named volumes; wipes evidence/audit chain)
docker compose down -v
```

> Note: the `make dev-down` target only invokes `docker compose down`; the
> `-v` flag must be passed to `docker compose` directly, not appended to
> `make dev-down`. `make dev-down -v` runs Make in verbose mode without
> dropping volumes.

---

## 7. Verifying P0 backend tests locally

If you want the same backend regression coverage CI runs:

```bash
# Edge core packages
go test -count=1 ./core/edge/...

# Gateway Edge handlers
go test -count=1 ./core/controlplane/gateway -run 'Test.*Edge'

# Edge regression suite (auth, tenant, limits, redaction, SK unavailable, stream, approval, export)
go test -count=3 ./core/controlplane/gateway -run 'Test.*Edge.*(Auth|Tenant|Limit|Redact|Unavailable|Stream|Approval|Export)'

# CLI binaries
go test -count=1 ./cmd/cordumctl/... ./cmd/cordum-hook/... ./cmd/cordum-agentd/...
```

Dashboard regression (run from `dashboard/`):

```bash
cd dashboard
node ./node_modules/typescript/bin/tsc --noEmit
npx vitest run
npm run build
```

All of the above should be green on `feature/cordum-edge-p0` HEAD.

---

## 8. Troubleshooting

| Symptom | Likely cause | Fix |
| --- | --- | --- |
| Script prints `SKIP edge_fake_hook_e2e` | `CORDUM_INTEGRATION` not set | `export CORDUM_INTEGRATION=1` then re-run. |
| `FAIL edge_fake_hook_e2e: CORDUM_API_KEY required in strict mode` | API key not exported, or doesn't match Gateway's `.env` value | Export the same key the stack was started with (`grep ^CORDUM_API_KEY= .env`). |
| `FAIL edge_pretooluse_deny: hook stdout permissionDecision != deny` | Demo Edge policy pack not installed → `default-allow` policy in effect | Run step 6 above: `./bin/cordumctl pack install ./examples/cordum-edge-pack` (with `CORDUM_TLS_CA` set to the dev CA cert). |
| `POST /api/v1/edge/sessions -> HTTP 401` | Wrong/missing API key in script env | As above; the value in `.env` is loaded by the Gateway container at start time. |
| `POST /api/v1/edge/sessions -> HTTP 404` | Gateway image pre-dates Edge work | `docker compose down -v && make dev-up` to rebuild from current source. |
| `curl: (60) SSL certificate problem` | Self-signed dev cert not trusted | Use `--cacert ./certs/ca/ca.crt` (preferred) or `-k` (insecure, dev only). |
| `make dev-up` hangs at "waiting for Redis" | Docker daemon not responsive | Restart Docker Desktop / `systemctl restart docker`; on Windows the `com.docker.service` Windows service must be running. |
| Dashboard at `:8082` shows blank Edge Sessions list | Stack started before script ran; refresh the page or run the script first. | Refresh after the fake-hook E2E completes. |
| `cordumctl edge claude` fails with `claude: command not found` | Claude Code not on PATH | `--claude-path /path/to/claude` or install Claude Code. |
| Fake-hook script complains about `jq` | `jq` missing from PATH | Install `jq`; on Windows `tools/scripts/jq.exe` is shipped as fallback. |
| `make: command not found` (Windows/MSYS) | GNU Make not installed | Use the `docker compose` / `go build` raw-command equivalents shown above, or install Make via `pacman -S make` in MSYS2. |

For deeper diagnostics:

```bash
./bin/cordumctl edge doctor
./bin/cordumctl edge doctor --json   # machine-readable
```

This runs eight observe-only health checks (binaries, agentd, Gateway,
settings, policy fixtures, connectivity) and reports `PASS`/`FAIL`/`WARN`
with remediation hints.

For the runbook of common failure modes during the demo, see
[`docs/edge/runbook.md`](edge/runbook.md). For full operator-facing API,
configuration, and CLI references, see
[`docs/edge/`](edge/).

---

## 9. What ships next (post-P0)

| Item | Status | Track |
| --- | --- | --- |
| Enterprise managed-settings deployment automation | Planned | EDGE-150 |
| Hook + agentd binary signing/notarization | Planned | EDGE-151 |
| Agentd keychain + service bootstrap hardening | Planned | EDGE-152 |
| MCP Gateway for cross-agent governance | Backlog | EDGE-100..105 (P1) |
| LLM Proxy (Anthropic Messages first) | Backlog | EDGE-120..124 (P2) |
| Shadow Agents detection (observe-mode P3) | Backlog | EDGE-140..144 (P3) |

The P0 surface is intentionally focused. Production deployment of the
wrapper alone is **not** an enterprise enforcement boundary — pair with
managed Claude settings and signed binaries before fleet rollout.

---

## Reference index

- Product overview: [`docs/edge/README.md`](edge/README.md)
- API reference: [`docs/edge/api.md`](edge/api.md)
- Configuration: [`docs/edge/configuration.md`](edge/configuration.md)
- CLI reference: [`docs/edge/cli.md`](edge/cli.md)
- Demo walkthrough: [`docs/edge/demo.md`](edge/demo.md)
- Operator runbook: [`docs/edge/runbook.md`](edge/runbook.md)
- Architecture decisions: [`docs/adr/010-edge-p0-architecture-decisions.md`](adr/010-edge-p0-architecture-decisions.md)
- Doctor diagnostics: [`docs/edge/cordumctl-edge-doctor.md`](edge/cordumctl-edge-doctor.md)
- Edge P0 threat model and acceptance evidence: internal Cordum engineering docs.
