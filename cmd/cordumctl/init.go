package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

const (
	initComposeTemplate = `version: "3.9"

services:
  nats:
    image: nats:2.10-alpine
    command: ["-js", "-sd", "/data"]
    ports:
      - "4222:4222"
    volumes:
      - nats_data:/data

  redis:
    image: redis:7-alpine
    ports:
      - "6379:6379"

  cordum-context-engine:
    image: cordum/control-plane:${CORDUM_VERSION:-latest}-context-engine
    depends_on:
      - redis
    environment:
      - REDIS_URL=redis://redis:6379
      - CONTEXT_ENGINE_ADDR=:50070
    ports:
      - "50070:50070"

  cordum-safety-kernel:
    image: cordum/control-plane:${CORDUM_VERSION:-latest}-safety-kernel
    restart: unless-stopped
    depends_on:
      - nats
    environment:
      - NATS_URL=nats://nats:4222
      - SAFETY_KERNEL_ADDR=:50051
      - SAFETY_POLICY_PATH=/etc/cordum/safety.yaml
    volumes:
      - ./config/safety.yaml:/etc/cordum/safety.yaml:ro
    ports:
      - "50051:50051"

  cordum-scheduler:
    image: cordum/control-plane:${CORDUM_VERSION:-latest}-scheduler
    restart: unless-stopped
    depends_on:
      - nats
      - redis
      - cordum-safety-kernel
    environment:
      - NATS_URL=nats://nats:4222
      - NATS_USE_JETSTREAM=1
      - REDIS_URL=redis://redis:6379
      - SAFETY_KERNEL_ADDR=cordum-safety-kernel:50051
      - POOL_CONFIG_PATH=/etc/cordum/pools.yaml
      - TIMEOUT_CONFIG_PATH=/etc/cordum/timeouts.yaml
      - JOB_META_TTL=168h
      - WORKER_SNAPSHOT_INTERVAL=5s
    volumes:
      - ./config/pools.yaml:/etc/cordum/pools.yaml:ro
      - ./config/timeouts.yaml:/etc/cordum/timeouts.yaml:ro

  cordum-api-gateway:
    image: cordum/control-plane:${CORDUM_VERSION:-latest}-api-gateway
    depends_on:
      - nats
      - redis
      - cordum-scheduler
    environment:
      - NATS_URL=nats://nats:4222
      - NATS_USE_JETSTREAM=1
      - REDIS_URL=redis://redis:6379
      - SAFETY_KERNEL_ADDR=cordum-safety-kernel:50051
      - API_KEY=${CORDUM_API_KEY:-super-secret-key}
      - CORDUM_API_KEY=${CORDUM_API_KEY:-super-secret-key}
      - CORDUM_SUPER_SECRET_API_TOKEN=${CORDUM_API_KEY:-super-secret-key}
      - TENANT_ID=default
      - API_RATE_LIMIT_RPS=50
      - API_RATE_LIMIT_BURST=100
      - REDIS_DATA_TTL=24h
      - JOB_META_TTL=168h
    ports:
      - "8080:8080"
      - "8081:8081"
      - "9092:9092"

  cordum-workflow-engine:
    image: cordum/control-plane:${CORDUM_VERSION:-latest}-workflow-engine
    depends_on:
      - nats
      - redis
      - cordum-scheduler
    environment:
      - NATS_URL=nats://nats:4222
      - NATS_USE_JETSTREAM=1
      - REDIS_URL=redis://redis:6379
      - WORKFLOW_ENGINE_HTTP_ADDR=:9093
      - WORKFLOW_ENGINE_SCAN_INTERVAL=5s
      - WORKFLOW_ENGINE_RUN_SCAN_LIMIT=200
    ports:
      - "9093:9093"

  cordum-dashboard:
    image: cordum/dashboard:${CORDUM_VERSION:-latest}
    depends_on:
      - cordum-api-gateway
    environment:
      - CORDUM_API_BASE_URL=${CORDUM_API_BASE_URL:-http://localhost:8081}
      - CORDUM_API_KEY=${CORDUM_API_KEY:-super-secret-key}
      - CORDUM_TENANT_ID=${CORDUM_TENANT_ID:-default}
      - CORDUM_PRINCIPAL_ID=${CORDUM_PRINCIPAL_ID:-}
      - CORDUM_PRINCIPAL_ROLE=${CORDUM_PRINCIPAL_ROLE:-}
    ports:
      - "8082:8080"

volumes:
  nats_data:
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
  running_timeout_seconds: 9000
  scan_interval_seconds: 30
`

	initSafetyTemplate = `default_tenant: default
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

## Start the stack

~~~bash
docker compose up -d
~~~

## Create the sample workflow

~~~bash
cordumctl workflow create --file workflows/hello.json
~~~

## Start a run and approve it

~~~bash
run_id=$(cordumctl run start <workflow_id>)
cordumctl approval step --approve <workflow_id> ${run_id} approve
~~~

## Open the dashboard

http://localhost:8082
`, filepath.Base(target))

	files := map[string]string{
		filepath.Join(target, "docker-compose.yml"):      initComposeTemplate,
		filepath.Join(target, "config", "pools.yaml"):    initPoolsTemplate,
		filepath.Join(target, "config", "timeouts.yaml"): initTimeoutsTemplate,
		filepath.Join(target, "config", "safety.yaml"):   initSafetyTemplate,
		filepath.Join(target, "workflows", "hello.json"): initWorkflowTemplate,
		filepath.Join(target, "README.md"):              readme,
	}
	for path, content := range files {
		if err := writeFile(path, content, force); err != nil {
			return err
		}
	}
	return nil
}
