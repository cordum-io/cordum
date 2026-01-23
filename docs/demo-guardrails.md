# Guardrails Demo (2 minutes)

This demo shows:
- Safety Kernel blocking a dangerous request
- A remediation suggestion
- An approval gate for risky work

If you want a local `cordumctl` binary:

```bash
make build SERVICE=cordumctl
```

## Prereqs

- Docker + Docker Compose
- curl + jq
- Go (for the demo worker)

## 1) Start the stack

```bash
go run ./cmd/cordumctl up
# or:
./bin/cordumctl up
# or:
docker compose up -d
```

## 2) Start the demo worker

```bash
cd examples/demo-guardrails/worker
REDIS_URL=${REDIS_URL:-redis://localhost:6379} \
NATS_URL=${NATS_URL:-nats://localhost:4222} \
go run .
```

## 3) Install the demo pack

```bash
./bin/cordumctl pack install --upgrade ./examples/demo-guardrails
```

If you didn't build the binary, use:

```bash
go run ./cmd/cordumctl pack install --upgrade ./examples/demo-guardrails
```

The demo script also installs the pack if it's missing.

## 4) Run the demo script

```bash
CORDUM_API_KEY=${CORDUM_API_KEY:-[REDACTED]} \
CORDUM_ORG_ID=${CORDUM_ORG_ID:-default} \
./tools/scripts/demo_guardrails.sh
```

One-command runner (starts the worker + demo):

```bash
./tools/scripts/demo_guardrails_run.sh
```

## Record a GIF

Option A: VHS (terminal GIF recorder)

```bash
# macOS: brew install vhs
# Linux: https://github.com/charmbracelet/vhs
vhs ./tools/scripts/demo_guardrails.tape
```

Option B: GUI recorder
- macOS: Kap
- Linux: Peek
- Windows: ShareX

Save the output as `docs/assets/guardrails-demo.gif`.
