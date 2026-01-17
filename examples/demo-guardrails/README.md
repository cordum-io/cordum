# Guardrails Demo

This pack demonstrates policy-before-dispatch + approval + remediation.

## Prereqs

- Cordum stack running (`cordumctl up` or `docker compose up -d`)
- A worker for the demo topics

## Run the demo

Terminal A (worker):

```bash
cd examples/demo-guardrails/worker
go run .
```

Terminal B (demo):

```bash
./cmd/cordumctl/cordumctl pack install ./examples/demo-guardrails --upgrade
./tools/scripts/demo_guardrails.sh
```

The script will:
- start a workflow run that pauses for approval
- approve the job
- submit a dangerous job that is denied
- apply the remediation to route to a safe job
