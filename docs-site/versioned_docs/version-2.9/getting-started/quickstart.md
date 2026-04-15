---
sidebar_position: 1
title: Quickstart
slug: /
---

# Quickstart

Get Cordum running locally in under 5 minutes.

## Prerequisites

- Docker and Docker Compose
- Go 1.24+ (for building from source)
- Node.js 20+ (for the dashboard)

## Start Cordum

```bash
git clone https://github.com/cordum-io/cordum.git
cd cordum
make dev-up
```

This starts all services: API Gateway, Scheduler, Safety Kernel, Workflow Engine, Context Engine, NATS, and Redis.

## Verify

```bash
curl http://localhost:8081/health
```

You should see `{"status":"ok"}`.

## Submit Your First Job

```bash
curl -X POST http://localhost:8081/api/v1/jobs \
  -H 'Content-Type: application/json' \
  -H 'X-Tenant-ID: default' \
  -d '{
    "topic": "job.default",
    "prompt": "Hello, Cordum!"
  }'
```

## Next Steps

- [Installation Guide](/getting-started/installation) for production deployments
- [Architecture](/concepts/architecture) to understand how Cordum works
- [Safety Kernel](/concepts/safety-kernel) for governance policies
