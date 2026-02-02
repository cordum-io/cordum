# Packs and Workers

Packs bundle workflows, policies, and worker capabilities. Workers subscribe to
job topics defined in a pack and publish results back to the bus.

## Install a pack

```bash
cordumctl pack install ./examples/hello-pack
```

Or via API:

```bash
curl -sS -X POST http://localhost:8081/api/v1/packs/install \
  -H "X-API-Key: ${CORDUM_API_KEY}" \
  -H "X-Tenant-ID: ${CORDUM_TENANT_ID}" \
  -F bundle=@./examples/hello-pack/pack.zip
```

## Run a worker

See the examples:

- `examples/hello-worker-go`
- `examples/python-worker`
- `examples/node-worker`

Workers should:

- Connect to NATS.
- Subscribe to the job topics defined by the pack.
- Publish results with job metadata.

See `docs/pack.md` for pack format and lifecycle rules.
