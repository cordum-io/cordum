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

The stack needs `CORDUM_API_KEY` and `REDIS_PASSWORD` (from your shell or
`.env`) and TLS certificates in `./certs/`. Export both values so the later
steps can use them too:

```bash
export CORDUM_API_KEY="$(openssl rand -hex 32)"
export REDIS_PASSWORD="$(openssl rand -hex 24)"

# cordumctl up generates ./certs/ when it's missing:
go run ./cmd/cordumctl up
# or:
./bin/cordumctl up
# or, with plain Compose, generate the certificates first:
go run ./cmd/cordumctl generate-certs
docker compose up -d
```

Put both values in `.env` too if you'll run `docker compose` from another
shell ([Installation, Option C](install.md#option-c-docker-compose-manual)).
`./tools/scripts/quickstart.sh` does all of this in one step: it creates
`./certs/`, writes any missing values to `.env` and starts the stack.

## 2) Start the demo worker

The default stack runs Redis and NATS **TLS-only**, so a manual worker run
must use the `rediss://` / `tls://` schemes and point at the auto-generated
CA (and client cert/key) via the `*_TLS_*` env vars the worker reads:

```bash
cd examples/demo-guardrails/worker
REDIS_URL=${REDIS_URL:-rediss://:${REDIS_PASSWORD}@localhost:6379} \
REDIS_TLS_CA=../../../certs/ca/ca.crt \
NATS_URL=${NATS_URL:-tls://localhost:4222} \
NATS_TLS_CA=../../../certs/ca/ca.crt \
NATS_TLS_CERT=../../../certs/client/tls.crt \
NATS_TLS_KEY=../../../certs/client/tls.key \
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
CORDUM_API_BASE=https://localhost:8081 \
CORDUM_API_KEY=${CORDUM_API_KEY:?set CORDUM_API_KEY} \
CORDUM_TENANT_ID=${CORDUM_TENANT_ID:-default} \
CORDUM_ORG_ID=${CORDUM_ORG_ID:-default} \
./tools/scripts/demo_guardrails.sh
```

The scripts default to `https://localhost:8081` and trust `./certs/ca/ca.crt`
when it exists (set `CORDUM_TLS_CA` to use another CA). If your gateway is not
on localhost (for example, running inside a Docker network), set the API base
and gateway:

```bash
CORDUM_API_BASE=https://cordum-api-gateway:8081 \
CORDUM_GATEWAY=https://cordum-api-gateway:8081 \
CORDUM_TLS_CA=/etc/cordum/tls/ca/ca.crt \
CORDUM_API_KEY=${CORDUM_API_KEY:?set CORDUM_API_KEY} \
CORDUM_TENANT_ID=${CORDUM_TENANT_ID:-default} \
CORDUM_ORG_ID=${CORDUM_ORG_ID:-default} \
./tools/scripts/demo_guardrails.sh
```

The gateway certificate covers the `cordum-api-gateway` network alias. The
Compose services read the CA from `./certs` mounted at `/etc/cordum/tls`; a
container of your own has no CA until you mount it the same way
(`-v "$PWD/certs:/etc/cordum/tls:ro"`).

One-command runner (starts the worker + demo):

```bash
CORDUM_API_BASE=https://localhost:8081 ./tools/scripts/demo_guardrails_run.sh
```

Note: the first run may take a bit while Go downloads modules and the worker
registers a heartbeat. The runner waits for a worker in the `demo-guardrails`
pool specifically, so unrelated worker heartbeats do not trigger a false ready
state.

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
