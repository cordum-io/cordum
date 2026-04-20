# Cordum CLI Commands Reference

Quick reference for common development operations.

## Development Lifecycle

### Starting Development Environment

```bash
# Start all services
docker compose up -d

# Verify services are running
docker compose ps

# View logs
docker compose logs -f api-gateway
docker compose logs -f cordum-scheduler

# Dashboard available at
open http://localhost:8082
```

### Building

```bash
# Build all binaries
make build

# Build specific service
make build SERVICE=cordum-api-gateway
make build SERVICE=cordum-scheduler
make build SERVICE=cordum-context-engine

# Build with race detector (for testing)
go build -race ./cmd/cordum-api-gateway

# Build Docker images
make docker SERVICE=cordum-api-gateway
docker compose build
```

### Testing

```bash
# Run all tests
go test ./...

# With local cache (avoids permission issues)
GOCACHE=$(pwd)/.cache/go-build go test ./...

# Run tests with coverage
go test -cover ./...
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# Run specific package tests
go test ./core/safety/...
go test ./core/workflow/...

# Run tests with verbose output
go test -v ./core/safety/...

# Run specific test
go test -v -run TestKernel_Evaluate ./core/safety/...

# Integration tests (requires Docker)
make test-integration

# Smoke tests
make smoke
./tools/scripts/platform_smoke.sh
./tools/scripts/cordumctl_smoke.sh

# Benchmark tests
go test -bench=. ./core/safety/...
```

## Protocol Buffers

```bash
# Regenerate all proto files
make proto

# Manual protoc command
protoc \
  --go_out=. --go_opt=paths=source_relative \
  --go-grpc_out=. --go-grpc_opt=paths=source_relative \
  core/protocol/proto/v1/*.proto

# Verify proto syntax
protoc --lint_out=. core/protocol/proto/v1/*.proto
```

## cordumctl Commands

```bash
# Install cordumctl
go install ./cmd/cordumctl

# View help
cordumctl --help

# Job operations
cordumctl job submit --topic job.hello-pack.echo --prompt "hello"
cordumctl job status <job-id>
cordumctl job logs <job-id>

# Workflow operations
cordumctl workflow create --file workflow.json
cordumctl workflow delete <workflow-id>
cordumctl run start <workflow-id> --input '{"key":"value"}'
cordumctl run get <run-id>
cordumctl run timeline <run-id>
cordumctl run delete <run-id>

# Approval operations
cordumctl approval job <job-id> --approve
cordumctl approval job <job-id> --reject
cordumctl approval repair <job-id>                # dry-run inspection
cordumctl approval repair <job-id> --apply
cordumctl approval repair <job-id> --apply --note "operator repair note"

# DLQ operations
cordumctl dlq retry <job-id>

# Pack operations
cordumctl pack list
cordumctl pack install ./my-pack
cordumctl pack show my-pack
cordumctl pack verify my-pack
cordumctl pack uninstall my-pack

# Topic registry
cordumctl topic list
cordumctl topic create job.my-pack.process --pool my-pack --input-schema my-pack/ProcessInput --output-schema my-pack/ProcessResult
cordumctl topic delete job.my-pack.process

# Worker credentials
cordumctl worker credential list
cordumctl worker credential create --worker-id external-worker-01 --allowed-pools my-pack --allowed-topics job.my-pack.process
cordumctl worker credential revoke --worker-id external-worker-01

# Pool management
cordumctl pool list
cordumctl pool get <pool-name>
cordumctl pool create <pool-name> --requires gpu,docker --description "GPU pool"
cordumctl pool update <pool-name> --description "Updated"
cordumctl pool delete <pool-name> --force
cordumctl pool drain <pool-name> --timeout 300
cordumctl pool topic add <pool-name> job.my-service.process
cordumctl pool topic remove <pool-name> job.my-service.process

# License management
cordumctl license info                    # display license details (plan, entitlements, expiry)
cordumctl license install ./license.json  # install license from file
cordumctl license reload                  # hot-reload license on running gateway (no restart)
cordumctl auth sso status                 # inspect published SAML metadata/login URLs and runtime state
cordumctl auth sso status --json          # raw /api/v1/auth/config output for automation
cordumctl status                          # show tier, expiry, usage vs limits

# Health & status
cordumctl status
cordumctl status --json                   # machine-readable output
cordumctl doctor                          # post-install / pre-upgrade verification
cordumctl doctor --json                   # machine-readable; wire into CI health gates
cordumctl doctor --strict                 # treat warns as fails (exit 1 on any non-ok)
cordumctl doctor --fix                    # interactive prompts per failing check
```

### `cordumctl doctor` — install verification

Runs a sequence of independent checks against a live deploy and reports
an actionable summary. Safe to run anytime — every probe is read-only
unless the operator opts in via `--fix`.

**When to run:**

| Situation | Command |
|-----------|---------|
| Right after `quickstart.sh` | `cordumctl doctor` (first green run = install succeeded) |
| CI health gate | `cordumctl doctor --json \| jq .exitCode` |
| Pre-upgrade sanity | `cordumctl doctor --strict` on the current version |
| Post-upgrade verification | `cordumctl doctor` + `cordumctl doctor --json` saved as artefact |
| Incident response | `cordumctl doctor --verbose` — full `DETAIL` per check + the fix hints |

**Checks shipped:**

| ID | What it tests | Fail fix |
|----|---------------|----------|
| `gateway_reachable` | `GET {gateway}/readyz` returns 200 | `docker compose up -d api-gateway` |
| `gateway_auth` | `GET /api/v1/status` with API key returns 200 | `export CORDUM_API_KEY=<your-key>` |
| `nats_connected` | `/api/v1/status` reports NATS connected | `docker compose logs nats` |
| `redis_ok` | `/api/v1/status` reports Redis OK | `docker compose logs redis` |
| `workers_registered` | `/api/v1/status` reports ≥1 worker | `cordumctl pack install ./demo/quickstart/pack` |
| `build_info` | gateway build version is not `dev`/empty | `docker compose pull && docker compose up -d` |
| `service_{scheduler,safety-kernel,context-engine,workflow-engine,mcp,dashboard}` | per-service readyz reachable from host | `docker compose logs <service>` |
| `demo_pack_installed` | quickstart demo pack present | `cordumctl pack install ./demo/quickstart/pack` |
| `policy_bundle_loaded` | ≥1 enabled policy bundle | `cordumctl policy activate <bundle-id>` |
| `version_skew` | cordumctl build matches gateway build | `docker compose pull && docker compose up -d` |
| `tls_cert_expiry` | `./certs/ca/ca.crt` valid >7 days | `cordumctl generate-certs --force --days 365` |

**State → exit-code mapping:**

| State | Meaning | Default exit | `--strict` exit |
|-------|---------|--------------|-----------------|
| `ok` | Check passed | 0 | 0 |
| `warn` | Non-fatal (unpinned image, missing demo pack, &c.) | 0 | 1 |
| `fail` | Something actually broken | 1 | 1 |
| `skip` | Precondition unmet (no API key, gateway down, service port not exposed) | 0 | 0 |

Exit code 2 is reserved for usage errors (unknown flag, `--fix` and `--json` combined, &c.).

**`--json` output schema:**

```json
{
  "checks": [
    {
      "id": "nats_connected",
      "label": "NATS connected",
      "state": "fail",
      "detail": "gateway reports NATS disconnected",
      "fix": "docker compose logs nats  (verify NATS_TOKEN + nats service health)"
    }
  ],
  "summary": { "ok": 12, "warn": 1, "fail": 1, "skip": 2 },
  "exitCode": 1
}
```

**`--fix` mode semantics:**

* Walks each `fail` check whose `fix` string is non-empty.
* Prints the suggested command and prompts `[y/N/a]` — default is skip.
* `y` runs the command via the platform shell, captures output, then
  re-runs the check to confirm the repair.
* `a` aborts all remaining fixes.
* If the fix contains a destructive substring (`--force`, `down -v`,
  `reset --hard`, `rm -rf`, `dropdb`, `DELETE FROM`) the operator must
  type `yes` at a second confirmation before the fix runs.
* `--fix` refuses to combine with `--json` (the interactive prompts
  would corrupt the machine-readable output).

**Required privileges:** the user running `cordumctl doctor` needs:

* Read access to the gateway (`CORDUM_API_KEY`, tenant).
* Read access to `./certs/ca/ca.crt` if TLS expiry check should run.
* With `--fix`: permission to run the commands embedded in `fix`
  strings (typically `docker compose` + `cordumctl` subcommands).

## Redis Operations

```bash
# Connect to Redis CLI
docker compose exec redis redis-cli

# View all keys
KEYS *

# View job-related keys
KEYS "job:*"
KEYS "ctx:*"
KEYS "res:*"
KEYS "workflow:*"

# Get specific job
GET job:<job-id>
HGETALL job:<job-id>

# View job queue
LRANGE jobs:pending 0 -1

# Clear all data (dev only!)
FLUSHALL

# Monitor commands in real-time
MONITOR
```

## NATS Operations

```bash
# Subscribe to all job topics
nats sub "job.>" --server=nats://localhost:4222

# Subscribe to specific topic
nats sub "job.echo.*" --server=nats://localhost:4222

# Subscribe to system topics
nats sub "sys.>" --server=nats://localhost:4222

# Publish test message
nats pub "job.echo.test" "hello" --server=nats://localhost:4222

# View JetStream streams
nats stream ls --server=nats://localhost:4222

# View stream info
nats stream info JOBS --server=nats://localhost:4222

# View consumers
nats consumer ls JOBS --server=nats://localhost:4222
```

## Metrics & Monitoring

```bash
# Scheduler metrics
curl http://localhost:9090/metrics

# API gateway metrics
curl http://localhost:9092/metrics

# Workflow engine health
curl http://localhost:9093/health

# Grep specific metrics
curl -s http://localhost:9090/metrics | grep cordum_jobs

# Watch metrics
watch -n 2 'curl -s http://localhost:9090/metrics | grep cordum_jobs'
```

## Docker Operations

```bash
# View running containers
docker compose ps

# View logs
docker compose logs -f <service>
docker compose logs --tail=100 api-gateway

# Restart service
docker compose restart cordum-scheduler

# Rebuild and restart
docker compose up -d --build api-gateway

# Shell into container
docker compose exec api-gateway sh
docker compose exec redis sh

# View container resource usage
docker stats

# Clean up
docker compose down
docker compose down -v  # Also removes volumes
docker system prune -f
```

## Git Operations

```bash
# Feature branch workflow
git checkout -b feature/my-feature
git add .
git commit -m "feat: add new feature"
git push origin feature/my-feature

# Conventional commits
git commit -m "feat: add new policy rule type"
git commit -m "fix: handle timeout in scheduler"
git commit -m "docs: update API documentation"
git commit -m "refactor: simplify workflow engine"
git commit -m "test: add safety kernel tests"
git commit -m "chore: update dependencies"

# Rebase on main
git fetch origin
git rebase origin/main
```

## Debugging

```bash
# Run with debug logging
LOG_LEVEL=debug go run ./cmd/cordum-scheduler

# Run with delve debugger
dlv debug ./cmd/cordum-api-gateway -- --config config.yaml

# Profile CPU
go test -cpuprofile cpu.prof -bench=. ./core/safety/...
go tool pprof cpu.prof

# Profile memory
go test -memprofile mem.prof -bench=. ./core/safety/...
go tool pprof mem.prof

# Trace execution
go test -trace trace.out ./core/safety/...
go tool trace trace.out
```

## Code Generation

```bash
# Generate mocks (using mockgen)
mockgen -source=core/safety/kernel.go -destination=core/safety/mocks/kernel_mock.go

# Generate from interfaces
go generate ./...

# Update go.sum
go mod tidy

# Vendor dependencies
go mod vendor
```

## Linting & Formatting

```bash
# Format Go code
gofmt -w .
goimports -w .

# Run linter
golangci-lint run

# Fix auto-fixable issues
golangci-lint run --fix

# Run specific linters
golangci-lint run --enable=govet,errcheck,staticcheck

# Format proto files
clang-format -i core/protocol/proto/v1/*.proto
```

## Dashboard Development

```bash
cd dashboard

# Install dependencies
npm install

# Development server
npm run dev

# Type checking
npm run typecheck

# Linting
npm run lint
npm run lint:fix

# Build
npm run build

# Test
npm test
npm run test:watch
npm run test:coverage

# Preview production build
npm run preview
```

## Kubernetes Operations

```bash
# Apply manifests
kubectl apply -f deploy/k8s/

# View pods
kubectl get pods -n cordum

# View logs
kubectl logs -f deployment/cordum-api -n cordum

# Port forward for local access
kubectl port-forward svc/cordum-api 8080:8080 -n cordum

# Scale deployment
kubectl scale deployment cordum-scheduler --replicas=3 -n cordum

# View events
kubectl get events -n cordum --sort-by='.lastTimestamp'
```

## Quick Aliases

Add to your shell rc file:

```bash
# Cordum aliases
alias cdm='cd ~/cordum'
alias cdmup='docker compose up -d'
alias cdmdown='docker compose down'
alias cdmlogs='docker compose logs -f'
alias cdmbuild='make build'
alias cdmtest='GOCACHE=$(pwd)/.cache/go-build go test ./...'
alias cdmsmoke='make smoke'

# cordumctl shortcuts
alias ctl='cordumctl'
alias ctljob='cordumctl job'
alias ctlwf='cordumctl workflow'
alias ctlappr='cordumctl approval'
```
