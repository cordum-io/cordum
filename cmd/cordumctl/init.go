package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

const (
	initComposeTemplate = `services:
  nats:
    image: nats:2.10-alpine
    command: ["-js", "-sd", "/data"]
    ports:
      - "4222:4222"
    volumes:
      - nats_data:/data
    healthcheck:
      test: ["CMD-SHELL", "nc -z localhost 4222"]
      interval: 10s
      timeout: 3s
      retries: 3

  redis:
    image: redis:7-alpine
    command: ["redis-server", "--appendonly", "yes", "--requirepass", "${REDIS_PASSWORD:-cordum-dev}"]
    ports:
      - "6379:6379"
    volumes:
      - redis_data:/data
    healthcheck:
      test: ["CMD", "redis-cli", "-a", "${REDIS_PASSWORD:-cordum-dev}", "ping"]
      interval: 10s
      timeout: 3s
      retries: 3

  cordum-context-engine:
    image: ghcr.io/cordum-io/cordum/control-plane:${CORDUM_VERSION:-latest}-context-engine
    depends_on:
      redis:
        condition: service_healthy
    environment:
      - REDIS_URL=redis://:${REDIS_PASSWORD:-cordum-dev}@redis:6379
      - CONTEXT_ENGINE_ADDR=:50070
    ports:
      - "50070:50070"
    healthcheck:
      test: ["CMD-SHELL", "nc -z localhost 50070"]
      interval: 10s
      timeout: 3s
      retries: 3

  cordum-safety-kernel:
    image: ghcr.io/cordum-io/cordum/control-plane:${CORDUM_VERSION:-latest}-safety-kernel
    restart: unless-stopped
    depends_on:
      nats:
        condition: service_healthy
      redis:
        condition: service_healthy
    environment:
      - NATS_URL=nats://nats:4222
      - REDIS_URL=redis://:${REDIS_PASSWORD:-cordum-dev}@redis:6379
      - SAFETY_KERNEL_ADDR=:50051
      - SAFETY_POLICY_PATH=/etc/cordum/safety.yaml
    volumes:
      - ./config/safety.yaml:/etc/cordum/safety.yaml:ro
    ports:
      - "50051:50051"
    healthcheck:
      test: ["CMD-SHELL", "nc -z localhost 50051"]
      interval: 10s
      timeout: 3s
      retries: 3

  cordum-scheduler:
    image: ghcr.io/cordum-io/cordum/control-plane:${CORDUM_VERSION:-latest}-scheduler
    restart: unless-stopped
    depends_on:
      nats:
        condition: service_healthy
      redis:
        condition: service_healthy
      cordum-safety-kernel:
        condition: service_healthy
    environment:
      - NATS_URL=nats://nats:4222
      - NATS_USE_JETSTREAM=1
      - REDIS_URL=redis://:${REDIS_PASSWORD:-cordum-dev}@redis:6379
      - SAFETY_KERNEL_ADDR=cordum-safety-kernel:50051
      - POOL_CONFIG_PATH=/etc/cordum/pools.yaml
      - TIMEOUT_CONFIG_PATH=/etc/cordum/timeouts.yaml
      - JOB_META_TTL=168h
      - WORKER_SNAPSHOT_INTERVAL=5s
    volumes:
      - ./config/pools.yaml:/etc/cordum/pools.yaml:ro
      - ./config/timeouts.yaml:/etc/cordum/timeouts.yaml:ro

  cordum-api-gateway:
    image: ghcr.io/cordum-io/cordum/control-plane:${CORDUM_VERSION:-latest}-api-gateway
    depends_on:
      nats:
        condition: service_healthy
      redis:
        condition: service_healthy
      cordum-scheduler:
        condition: service_started
    environment:
      - NATS_URL=nats://nats:4222
      - NATS_USE_JETSTREAM=1
      - REDIS_URL=redis://:${REDIS_PASSWORD:-cordum-dev}@redis:6379
      - SAFETY_KERNEL_ADDR=cordum-safety-kernel:50051
      - CORDUM_API_KEY=${CORDUM_API_KEY:?error: CORDUM_API_KEY is not set}
      - TENANT_ID=default
      - API_RATE_LIMIT_RPS=2000
      - API_RATE_LIMIT_BURST=4000
      - REDIS_DATA_TTL=24h
      - JOB_META_TTL=168h
    ports:
      - "8080:8080"
      - "8081:8081"
      - "9092:9092"
    healthcheck:
      test: ["CMD-SHELL", "nc -z localhost 8080"]
      interval: 10s
      timeout: 3s
      retries: 3

  cordum-workflow-engine:
    image: ghcr.io/cordum-io/cordum/control-plane:${CORDUM_VERSION:-latest}-workflow-engine
    depends_on:
      nats:
        condition: service_healthy
      redis:
        condition: service_healthy
      cordum-scheduler:
        condition: service_started
    environment:
      - NATS_URL=nats://nats:4222
      - NATS_USE_JETSTREAM=1
      - REDIS_URL=redis://:${REDIS_PASSWORD:-cordum-dev}@redis:6379
      - WORKFLOW_ENGINE_HTTP_ADDR=:9093
      - WORKFLOW_ENGINE_SCAN_INTERVAL=5s
      - WORKFLOW_ENGINE_RUN_SCAN_LIMIT=200
    ports:
      - "9093:9093"
    healthcheck:
      test: ["CMD-SHELL", "nc -z localhost 9093"]
      interval: 10s
      timeout: 3s
      retries: 3

  cordum-dashboard:
    image: ghcr.io/cordum-io/cordum/dashboard:${CORDUM_VERSION:-latest}
    depends_on:
      cordum-api-gateway:
        condition: service_healthy
    environment:
      - CORDUM_API_BASE_URL=${CORDUM_API_BASE_URL:-http://localhost:8081}
      - CORDUM_API_KEY=${CORDUM_API_KEY:-}
      - CORDUM_TENANT_ID=${CORDUM_TENANT_ID:-default}
      - CORDUM_PRINCIPAL_ID=${CORDUM_PRINCIPAL_ID:-}
      - CORDUM_PRINCIPAL_ROLE=${CORDUM_PRINCIPAL_ROLE:-}
    ports:
      - "8082:8080"
    healthcheck:
      test: ["CMD-SHELL", "wget -q --spider http://localhost:8080 || exit 1"]
      interval: 10s
      timeout: 3s
      retries: 3

volumes:
  nats_data:
  redis_data:
`

	initPoolsTemplate = `topics:
  job.default: default
pools:
  default:
    requires: []
`

	initTimeoutsTemplate = `workflows: {}
topics: {}
reconciler:
  dispatch_timeout_seconds: 300
  # Per-topic overrides available via topics.<topic>.running_timeout_seconds
  running_timeout_seconds: 900
  scan_interval_seconds: 30
`

	initSafetyTemplate = `# Fail-closed: unmatched jobs are denied. Add allow rules for safe topics.
default_decision: deny
output_policy:
  enabled: true
  fail_mode: closed
default_tenant: default
tenants:
  default:
    allow_topics:
      - "job.*"
    deny_topics:
      - "sys.*"
    allowed_repo_hosts: []
    denied_repo_hosts: []
    mcp:
      allow_servers: []
      deny_servers: []
      allow_tools: []
      deny_tools: []
      allow_resources: []
      deny_resources: []
      allow_actions: []
      deny_actions: []
`

	initWorkflowTemplate = `{
  "name": "hello-world",
  "org_id": "default",
  "steps": {
    "approve": {
      "type": "approval",
      "name": "Approve"
    }
  }
}
`
)

func runInitCmd(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	force := fs.Bool("force", false, "overwrite existing files")
	if err := fs.Parse(args); err != nil {
		fail(err.Error())
	}
	if fs.NArg() < 1 {
		fail("project directory required")
	}
	target := fs.Arg(0)
	if err := scaffoldInit(target, *force); err != nil {
		fail(err.Error())
	}
	fmt.Printf("Cordum project initialized at %s\n", target)
}

func scaffoldInit(target string, force bool) error {
	info, err := os.Stat(target)
	if err == nil && !info.IsDir() {
		return fmt.Errorf("not a directory: %s", target)
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := ensureDir(target); err != nil {
		return err
	}

	readme := fmt.Sprintf(`# %s

Local Cordum project scaffold.

## Prerequisites

- Docker and Docker Compose (v2+)
- ` + "`openssl`" + ` for API key generation

## Configuration

Create a ` + "`.env`" + ` file (or export variables) before starting:

` + "```" + `
# Required: API key for the gateway (generate with: openssl rand -hex 32)
CORDUM_API_KEY=<your-generated-key>

# Optional: override the default dev Redis password (default: cordum-dev)
REDIS_PASSWORD=cordum-dev

# Optional: pin a specific release version
CORDUM_VERSION=latest
` + "```" + `

> **Production**: You MUST change REDIS_PASSWORD to a strong random value
> and generate a unique CORDUM_API_KEY. The defaults are for local development only.
>
> **Kubernetes**: API keys are stored in K8s secrets. Retrieve with:
> ` + "`kubectl get secret cordum-api-key -n cordum -o jsonpath='{.data.API_KEY}' | base64 -d`" + `

## Start the stack

` + "```bash" + `
# Generate an API key
export CORDUM_API_KEY="$(openssl rand -hex 32)"

# Start all services
docker compose up -d

# Verify everything is healthy
docker compose ps
` + "```" + `

## Create the sample workflow

` + "```bash" + `
cordumctl workflow create --file workflows/hello.json
` + "```" + `

## Start a run and approve it

` + "```bash" + `
run_id=$(cordumctl run start <workflow_id>)
cordumctl approval step --approve <workflow_id> ${run_id} approve
` + "```" + `

## Open the dashboard

http://localhost:8082
`, filepath.Base(target))

	files := map[string]string{
		filepath.Join(target, "docker-compose.yml"):      initComposeTemplate,
		filepath.Join(target, "config", "pools.yaml"):    initPoolsTemplate,
		filepath.Join(target, "config", "timeouts.yaml"): initTimeoutsTemplate,
		filepath.Join(target, "config", "safety.yaml"):   initSafetyTemplate,
		filepath.Join(target, "workflows", "hello.json"): initWorkflowTemplate,
		filepath.Join(target, "README.md"):               readme,
	}
	for path, content := range files {
		if err := writeFile(path, content, force); err != nil {
			return err
		}
	}
	return nil
}
