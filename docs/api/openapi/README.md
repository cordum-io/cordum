# OpenAPI

This directory contains the **canonical** Cordum HTTP OpenAPI spec:

| File | Role |
|------|------|
| `cordum-api.yaml` | Source-of-truth OpenAPI 3.0.3 spec for the gateway HTTP surface |
| `index.html` | Single-spec Swagger UI wrapper for browsing `cordum-api.yaml` locally |

## Viewing the spec

Open `index.html` in a browser, or serve the directory locally:

```bash
cd docs/api/openapi
python -m http.server 8000
# Open http://localhost:8000
```

## Validating the canonical spec

```bash
make openapi
```

`make openapi` now runs Redocly lint against `docs/api/openapi/cordum-api.yaml`.
It does **not** regenerate secondary Swagger artifacts.

## Maintaining the canonical HTTP spec

When gateway routes or schemas change:

1. Check `core/controlplane/gateway/gateway.go` and the relevant handler files
   for the live HTTP surface.
2. Update `docs/api/openapi/cordum-api.yaml`.
3. Validate with `make openapi`.
4. When auditing route/spec coverage, also run:
   ```bash
   go run ./tools/openapi-audit --spec docs/api/openapi/cordum-api.yaml --gateway-dir core/controlplane/gateway
   ```

There is no longer a separate hand-maintained REST spec or protobuf-generated
Swagger JSON in this directory.
