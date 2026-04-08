# cordumctl

Command-line helper for local dev, workflows, and pack operations.

## Global flags

- `--gateway` (or `CORDUM_GATEWAY`) default: `http://localhost:8081`
- `--api-key` (or `CORDUM_API_KEY`) default: empty
- `--tenant` (or `CORDUM_TENANT_ID`) default: `default`
- `--cacert` (or `CORDUM_TLS_CA`) default: empty
- `--insecure` (or `CORDUM_TLS_INSECURE`) default: `false`

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

## Topics

```bash
cordumctl topic list
cordumctl topic create job.my-pack.process --pool my-pack
cordumctl topic create job.my-pack.process --pool my-pack --input-schema my-pack/ProcessInput --output-schema my-pack/ProcessResult
cordumctl topic delete job.my-pack.process
```

Use topic registrations to populate the canonical topic registry consumed by the
gateway, scheduler, and dashboard Topics page.

## Worker credentials

```bash
cordumctl worker credential list
cordumctl worker credential create --worker-id external-worker-01 --allowed-pools my-pack --allowed-topics job.my-pack.process
cordumctl worker credential revoke --worker-id external-worker-01
```

`worker credential create` prints the plaintext token once. Store it securely before
starting or rotating the worker.

## Packs

```bash
cordumctl pack create my-pack
cordumctl pack install ./my-pack
cordumctl pack list
cordumctl pack show my-pack
cordumctl pack verify my-pack
cordumctl pack uninstall my-pack
```

## Pools

```bash
cordumctl pool list
cordumctl pool get my-pack
cordumctl pool create my-pack --description "My pack workers"
cordumctl pool topic add my-pack job.my-pack.process
cordumctl pool topic remove my-pack job.my-pack.process
cordumctl pool drain my-pack --timeout 300
```
