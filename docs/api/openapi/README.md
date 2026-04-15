# OpenAPI Specs

This directory contains the canonical HTTP OpenAPI specification plus the generated protobuf swagger subset:

| File | Source | Description |
|------|--------|-------------|
| `cordum-api.yaml` | Hand-maintained | Canonical OpenAPI 3.0.3 spec for the full gateway HTTP surface |
| `cordum.swagger.json` | Generated from protobufs | Legacy gRPC gateway swagger subset |
| `cordum-rest.yaml` | Hand-maintained (legacy) | Earlier REST spec retained for reference while downstream tooling migrates |

## Viewing the specs

Open `index.html` in a browser. A dropdown at the top lets you switch between the canonical HTTP spec and the protobuf-generated swagger subset.

To serve locally:

```bash
cd docs/api/openapi
python -m http.server 8000
# Open http://localhost:8000
```

## Spec roles

- `cordum-api.yaml` is the source of truth for the gateway HTTP API.
- `cordum.swagger.json` is still generated from protobuf definitions for the gRPC-transcoded subset and should be treated as a secondary artifact, not the full contract.

## Generating the gRPC spec

```bash
make openapi
```

This runs `protoc` with the `openapiv2` plugin, emits `cordum.swagger.json`, and validates `cordum-api.yaml` with Redocly.

## Maintaining the canonical HTTP spec

`cordum-api.yaml` is manually maintained. When gateway routes change:

1. Check `core/controlplane/gateway/gateway.go` for route registrations
2. Check handler files in `core/controlplane/gateway/` for request/response shapes
3. Update the relevant path and schema entries in `cordum-api.yaml`
4. Validate:
   ```bash
   npx --yes @redocly/cli@latest lint docs/api/openapi/cordum-api.yaml
   ```

### Structure of cordum-api.yaml

- **info** — title, version, description
- **tags** — logical gateway domains (Auth, Jobs, Workflows, Policy, Workers, MCP, etc.)
- **paths** — full gateway route inventory, including versioned and legacy MCP aliases
- **components/securitySchemes** — `apiKey` (X-API-Key header) and `bearerAuth` (JWT)
- **components/schemas** — reusable request/response schema definitions

### Adding a new endpoint

1. Add the path under the appropriate comment section
2. Use an existing schema or define a new one under `components/schemas`
3. Tag it with the correct group
4. Set `operationId` to a unique camelCase identifier
5. Run the validation command above
