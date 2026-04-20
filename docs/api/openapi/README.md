# OpenAPI Specs

This directory contains the canonical HTTP OpenAPI specification plus the generated protobuf swagger subset:

| File | Source | Role |
|------|--------|------|
| `cordum-api.yaml` | Hand-maintained | **Authoritative** — the canonical OpenAPI 3.0.3 spec for the full gateway HTTP surface. SDK generators (`@cordum/sdk`, `cordum-sdk-py`) and oasdiff breaking-change gates read this file. |
| `cordum.swagger.json` | Generated from protobufs via `make openapi` | **Advisory** — the gRPC-transcoded subset. Useful for grpc-gateway clients but NOT a full contract; treat it as secondary to `cordum-api.yaml`. |
| `cordum-rest.yaml` | Hand-maintained (legacy) | Older REST draft retained while downstream tooling migrates. Do not extend. |

## Viewing the specs

Open `index.html` in a browser. A dropdown at the top lets you switch between the canonical HTTP spec and the protobuf-generated swagger subset.

To serve locally:

```bash
cd docs/api/openapi
python -m http.server 8000
# Open http://localhost:8000
```

## Validation pipeline

Three checks run against `cordum-api.yaml`:

1. **`redocly lint`** — schema-level OpenAPI validation and style rules.
2. **`openapi-audit`** (this repo, `tools/openapi-audit/`) — diffs every `mux.HandleFunc` registration under `core/controlplane/gateway/` against the spec's `paths.*.<method>` operations. Fails if any gateway route is missing from the spec, or any spec op points at a path that isn't routed.
3. **`oasdiff breaking --fail-on ERR`** — compares the current spec against the committed base (default `origin/main`) and fails on any backward-incompatible change — removed operation, narrowed response schema, tightened enum, new required field on an existing request body, etc.

Run the full pipeline locally:

```bash
make openapi-validate
```

Or each piece on its own:

```bash
make openapi-audit                                        # route<->spec coverage only
npx --yes @redocly/cli@latest lint docs/api/openapi/cordum-api.yaml   # lint only
go install github.com/tufin/oasdiff@latest && \
  oasdiff breaking --fail-on ERR \
    <(git show origin/main:docs/api/openapi/cordum-api.yaml) \
    docs/api/openapi/cordum-api.yaml                       # breaking-change check
```

In CI, the `openapi` job wires all three into a single gate. See `.github/workflows/ci.yml`.

### Go-test layer

`core/controlplane/gateway/openapi_coverage_test.go::TestOpenAPICoverage`
runs `openapi-audit` at `go test` time, so drift is caught locally without
needing a CI round-trip. `TestOpenAPIRedoclyLint` runs the redocly lint,
gated on `OPENAPI_FULL=1` so the default `go test ./...` stays hermetic.

## Generating the gRPC spec

```bash
make openapi
```

This runs `protoc` with the `openapiv2` plugin, emits `cordum.swagger.json`, and validates `cordum-api.yaml` with Redocly.

## Maintaining the canonical HTTP spec

`cordum-api.yaml` is manually maintained. When gateway routes change:

1. Add the `mux.HandleFunc("METHOD /api/...", ...)` registration in `core/controlplane/gateway/gateway.go` (or a sibling `handlers_*.go` file).
2. Add the matching `paths.<path>.<method>` entry to `cordum-api.yaml`.
3. Add or reuse schema entries under `components.schemas` — required fields come from the Go struct's non-omitempty `json` tags.
4. Run `make openapi-validate`.

### Structure of cordum-api.yaml

- **info** — title, version, description.
- **tags** — logical gateway domains (Auth, Jobs, Workflows, Policy, Workers, MCP, Agents, etc.).
- **paths** — full gateway route inventory, including versioned and legacy MCP aliases.
- **components/securitySchemes** — `apiKey` (X-API-Key header) and `bearerAuth` (JWT). Declared top-level in `security:` so every op inherits both unless it overrides.
- **components/schemas** — reusable request/response schema definitions.

### Adding a new endpoint

1. Add the path under the appropriate comment section.
2. Use an existing schema or define a new one under `components/schemas`.
3. Tag it with the correct group.
4. Set `operationId` to a unique camelCase identifier.
5. Run `make openapi-validate`.

### Handling intentional breaking changes

oasdiff is strict: schema narrowing, removed operations, and tightened
enum sets all fail CI by default. When a breaking change IS intended
(e.g. a deliberate API v2), include the string
`allow-breaking-openapi` anywhere in the HEAD commit message of the
branch. The `openapi` CI job reads the HEAD commit message and sets
`OPENAPI_ALLOW_BREAKING=1`, which downgrades `oasdiff breaking` to a
reporting-only step. Reviewers still see the diff; the build just
doesn't fail on it.

### Handling dead paths

If a route is renamed or removed and the old operation must stay
callable in the spec for SDK compatibility, mark it with
`deprecated: true`. The `openapi-audit` tool skips deprecated ops when
computing route<->spec coverage, so renaming a route does not require
ripping the old stanza out (and therefore does not trigger an oasdiff
"removed operation" break). Add a cross-reference to the replacement
path in the `description:` field.

### Special-case: WebSocket endpoints

Paths registered without a method prefix
(`mux.HandleFunc("/api/v1/stream", ...)` — Go 1.22 any-method form)
must declare `x-any-method: true` at the path level so the audit tool
accepts the single `get:` stanza as covering every verb. Use
`x-websocket: true` and `x-websocket-message-schema` on the path to
expose the frame schema for dashboard / SDK tooling.

## Audit trail

Per-audit artifacts live alongside the spec:

- `AUDIT_BASELINE.md` — the route<->spec gap list at the start of the latest audit.
- `SCHEMA_DRIFT.md` — per-tag drift findings with file:line pointers.
- `ERROR_CODE_AUDIT.md` — observed-vs-documented HTTP status distribution across handlers.
- `CHANGELOG.md` — dated log of every spec revision.
