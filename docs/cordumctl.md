# cordumctl

Command-line helper for local dev, workflows, and pack operations.

## Global flags

- `--gateway` (or `CORDUM_GATEWAY`) default: `http://localhost:8081`
- `--api-key` (or `CORDUM_API_KEY`) default: empty

## Install/build

From repo root:

```bash
make build SERVICE=cordumctl
```

Binary is emitted at `bin/cordumctl`.

For one-off runs without building:

```bash
go run ./cmd/cordumctl <args>
```

## Project setup

```bash
cordumctl init my-project
cd my-project
docker compose up -d
```

## Dev and status

```bash
cordumctl dev --file docker-compose.yml
cordumctl status
```

## Workflows and runs

```bash
cordumctl workflow create --file workflow.json
cordumctl workflow delete <workflow_id>
cordumctl run start --input input.json <workflow_id>
cordumctl run start --dry-run <workflow_id>
cordumctl run timeline <run_id>
cordumctl run delete <run_id>
cordumctl approval step --approve <workflow_id> <run_id> <step_id>
cordumctl approval job --approve <job_id>
cordumctl approval job --reject <job_id>
```

## Jobs

```bash
cordumctl job submit --topic job.hello.world --prompt "hello" --input '{"name":"Yaron"}'
cordumctl job status <job_id>
cordumctl job logs <job_id>
```

## DLQ

```bash
cordumctl dlq retry <job_id>
```

## Packs

```bash
cordumctl pack create my-pack
cordumctl pack install ./my-pack
cordumctl pack list
cordumctl pack show my-pack
cordumctl pack verify my-pack
cordumctl pack uninstall my-pack
```
