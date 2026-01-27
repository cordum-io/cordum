# Demos

## Guardrails demo

Shows Safety Kernel approvals and remediation.

```bash
export CORDUM_API_KEY="$(openssl rand -hex 32)"
export CORDUM_TENANT_ID=default
./tools/scripts/demo_guardrails_run.sh
```

## Mock bank demo

Governed banking workflow demo.

```bash
export CORDUM_API_KEY="$(openssl rand -hex 32)"
export CORDUM_TENANT_ID=default
./tools/scripts/demo_mock_bank.sh
```
