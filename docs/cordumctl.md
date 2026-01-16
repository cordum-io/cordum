# cordumctl

Command-line helper for local dev, workflows, and pack operations.

## Global flags

- `--gateway` (or `CORDUM_GATEWAY`) default: `http://localhost:8081`
- `--api-key` (or `CORDUM_API_KEY`) default: empty

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
cordumctl run start <workflow_id> --input input.json
cordumctl run timeline <run_id>
cordumctl approval step --approve <workflow_id> <run_id> <step_id>
```

## Jobs

```bash
cordumctl job submit --topic job.hello.world --prompt "hello" --input '{"name":"Yaron"}'
cordumctl job status <job_id>
cordumctl job logs <job_id>
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
