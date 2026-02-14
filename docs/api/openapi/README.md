# OpenAPI Specs

This directory contains two OpenAPI specifications for the Cordum platform:

| File | Source | Description |
|------|--------|-------------|
| `cordum-rest.yaml` | Hand-maintained | OpenAPI 3.0.3 spec for all REST/HTTP endpoints |
| `cordum.swagger.json` | Generated from protobufs | gRPC gateway swagger spec |

## Viewing the specs

Open `index.html` in a browser. A dropdown at the top lets you switch between the REST spec and the gRPC gateway spec.

To serve locally:

```bash
cd docs/api/openapi
python -m http.server 8000
# Open http://localhost:8000
```

## Generating the gRPC spec

```bash
make openapi
```

This runs `protoc` with the `openapiv2` plugin and emits `cordum.swagger.json`.

## Maintaining the REST spec

`cordum-rest.yaml` is manually maintained. When gateway routes change:

1. Check `core/controlplane/gateway/gateway_core.go` for route registrations
2. Check handler files (`gateway_jobs.go`, `gateway_mcp.go`, etc.) for request/response shapes
3. Update the relevant path and schema entries in `cordum-rest.yaml`
4. Validate:
   ```bash
   python -c "import yaml; yaml.safe_load(open('docs/api/openapi/cordum-rest.yaml'))"
   ```

### Structure of cordum-rest.yaml

- **info** — title, version, description
- **tags** — 18 logical groups (Auth, Jobs, Workflows, Policy, etc.)
- **paths** — 75 endpoint definitions with operationId, parameters, requestBody, responses
- **components/securitySchemes** — `apiKey` (X-API-Key header) and `bearerAuth` (JWT)
- **components/schemas** — 57 reusable schema definitions

### Adding a new endpoint

1. Add the path under the appropriate comment section
2. Use an existing schema or define a new one under `components/schemas`
3. Tag it with the correct group
4. Set `operationId` to a unique camelCase identifier
5. Run the YAML validation command above
