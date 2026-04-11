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
cordumctl status                          # show tier, expiry, usage vs limits

# Health & status
cordumctl status
cordumctl status --json                   # machine-readable output
```

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
