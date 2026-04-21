# OpenAPI Legacy Surface Audit — 2026-04-20

Durable record for epic `epic-1cadd6f2`, task `task-d9d7f428` before deleting
legacy OpenAPI artifacts and MCP HTTP aliases.

## Scope

This audit answers five questions before any deletion:

1. What still generates or serves `cordum.swagger.json`?
2. What still references `cordum-rest.yaml`?
3. Does the canonical spec still carry legacy-only copy or deprecated fields?
4. Does `gateway.go` still register legacy MCP aliases?
5. Do the Python and TypeScript SDKs still consume only `cordum-api.yaml`?

## Current file inventory

Primary repo (`cordum/docs/api/openapi/`):

| File | Size | Status | Notes |
|------|------|--------|-------|
| `cordum-api.yaml` | 172,170 bytes | **Canonical** | Current OpenAPI 3.0.3 source of truth. |
| `cordum-rest.yaml` | 110,889 bytes | **Legacy** | Hand-maintained REST-only spec slated for deletion. |
| `cordum.swagger.json` | 12,826 bytes | **Legacy generated artifact** | Produced by `tools/scripts/gen_openapi.sh` via `protoc-gen-openapiv2`. |
| `index.html` | 2,059 bytes | Needs update | Swagger UI dropdown still offers the two legacy artifacts. |
| `README.md` | 1,906 bytes | Needs update | Still documents a dual-spec model. |

Published docs mirror (`D:/Cordum/Cordum-site/docs-site/static/swagger/`):

| File | Size | Status |
|------|------|--------|
| `cordum-api.yaml` | 164,823 bytes | Canonical mirror |
| `cordum-rest.yaml` | 110,889 bytes | Legacy mirror |
| `cordum.swagger.json` | 8,657 bytes | Legacy mirror |
| `index.html` | 2,708 bytes | Still offers legacy choices |

## Generator and build-path audit

`tools/scripts/gen_openapi.sh` still performs protobuf OpenAPI generation:

```bash
protoc \
  -I . \
  -I "$ROOT_DIR" \
  --openapiv2_out="$OUT_DIR" \
  --openapiv2_opt=...,allow_merge=true,merge_file_name=cordum \
  "${PROTO_FILES[@]}"
```

Observed behavior:

- `merge_file_name=cordum` means the protobuf pipeline still emits
  `docs/api/openapi/cordum.swagger.json`.
- The script then lints **`cordum-api.yaml`**, so the build currently mixes
  a legacy generated artifact with a separate canonical handwritten spec.
- This is why deleting `cordum.swagger.json` alone would churn back on the next
  `make openapi`.

## Canonical-spec audit (`cordum-api.yaml`)

### Legacy copy still present

The top-level `info.description` still says:

- `Canonical OpenAPI 3.0.3 spec for the Cordum gateway HTTP surface, including all REST routes in gateway.go plus legacy and prefixed MCP HTTP endpoints.`

That phrase was stale and has now been removed from
`docs/api/openapi/cordum-api.yaml`.

### Deprecated field check

The implementation plan expected one remaining `deprecated: true` field on the
incident-extraction request. The current tree no longer matches that assumption.

Actual result on 2026-04-20:

- `Select-String -Path docs/api/openapi/cordum-api.yaml -Pattern 'deprecated'`
  returned **no matches**.
- `rg -n "incident|Incident|dataset|/api/v1/evals" docs/api/openapi/cordum-api.yaml`
  returned **no matches**.

Conclusion: there is **no remaining `deprecated: true` marker in the current
canonical spec**. Step 3 should therefore remove only the stale MCP description
copy unless a concurrent change reintroduces a deprecated field.

### Incident-extraction tenant caller check

The implementation plan also called out an ignored legacy `tenant` body field on
the incident-extraction endpoint. Current repo state:

- The handler still accepts `tenant` on the internal request struct in
  `core/controlplane/gateway/handlers_evals_extraction.go`, then overwrites it
  from auth middleware (`req.Tenant = tenant`).
- The only concrete caller still sending a request body `tenant` field in this
  repo is the **gateway unit test** at
  `core/controlplane/gateway/handlers_evals_extraction_test.go`, which
  intentionally asserts middleware precedence.
- CLI, dashboard, and Go SDK callers do **not** send a tenant body field:
  `cmd/cordumctl/evals.go`, `dashboard/src/hooks/useEvals.ts`,
  `dashboard/src/api/types.ts`, and `sdk/client/evals_extract_types.go` all omit
  it.
- The current canonical spec does not expose the eval dataset extraction routes
  at all, so there is no spec-side `tenant` property to delete today.

Conclusion: there is no external caller migration needed for this task. The
ignored body field remains an implementation detail/test fixture outside the
legacy-spec deletion scope.

## MCP route-alias audit

### `gateway.go`

`gateway.go` contains **no direct MCP alias registration strings**:

- `rg -n '"/mcp/|/api/v1/mcp/' core/controlplane/gateway/gateway.go` → no matches

So the task-plan hypothesis is correct for `gateway.go` itself: there are no
legacy MCP alias registrations there.

### Actual alias registrations in the gateway package

Dual-registration does still exist in `core/controlplane/gateway/handlers_mcp.go`
inside `registerMCPRoutes`:

- `GET /mcp/sse`
- `POST /mcp/message`
- `GET /mcp/status`
- `GET /api/v1/mcp/sse`
- `POST /api/v1/mcp/message`
- `GET /api/v1/mcp/status`

Interpretation:

- `gateway.go` is clean.
- The live legacy/prefixed alias surface is owned by `handlers_mcp.go`, not
  by `gateway.go`.
- Any deletion step for legacy MCP aliases must touch `registerMCPRoutes`, not
  just the canonical spec copy.

## Downstream reference audit

Non-versioned files in the current repo that still point at legacy artifacts:

| File | Legacy reference |
|------|------------------|
| `docs/api.md` | says `make openapi` writes merged output `cordum.swagger.json` |
| `docs/api-reference.md` | points at `docs/api/openapi/cordum.swagger.json` |
| `docs/api/openapi/README.md` | documents `cordum-rest.yaml` + `cordum.swagger.json` |
| `docs/api/openapi/index.html` | dropdown offers `cordum-rest.yaml` + `cordum.swagger.json` |
| `docs-site/docs/api-reference/api-overview.md` | says output is `cordum.swagger.json` |
| `docs-site/docs/api-reference/full-reference.md` | points at `cordum.swagger.json` |
| `docs-site/docs/api-reference/rest-api.md` | links to `cordum-rest.yaml` |

Non-versioned files in `D:/Cordum/Cordum-site` that still point at legacy artifacts:

| File | Legacy reference |
|------|------------------|
| `Cordum-site/docs-site/docs/api-reference/api-overview.md` | links to `/swagger/cordum.swagger.json` |
| `Cordum-site/docs-site/docs/api-reference/full-reference.md` | says output/raw spec are `cordum.swagger.json` |
| `Cordum-site/docs-site/static/swagger/index.html` | dropdown still includes `cordum.swagger.json` |
| `Cordum-site/docs-site/static/swagger/cordum-api.yaml` | still carries the stale `legacy and prefixed MCP HTTP endpoints` text |

Out-of-scope but observed:

- `docs-site/versioned_docs/version-2.9/...` still references the legacy specs.
- The epic rail for this task explicitly excludes `versioned_docs/`; leave them
  to the sibling task.

## SDK consumer audit

Python and TypeScript SDK work already points only at the canonical spec:

### Python SDK

Confirmed references to `docs/api/openapi/cordum-api.yaml` in:

- `sdk/python/scripts/generate.sh`
- `sdk/python/scripts/generate.ps1`
- `sdk/python/tests/test_generated_coverage.py`
- `sdk/python/README.md`
- `sdk/python/CHANGELOG.md`

### TypeScript SDK

Confirmed references to `docs/api/openapi/cordum-api.yaml` in:

- `sdk/typescript/scripts/generate.mjs`
- `sdk/typescript/scripts/check-generated.mjs`
- `sdk/typescript/tests/generated_coverage.test.ts`
- `sdk/typescript/README.md`
- `sdk/typescript/src/_generated/README.md`

### Negative check

`rg -n "cordum-rest\.yaml|cordum\.swagger\.json" sdk ...` returned **no SDK
references** to the legacy spec artifacts.

Conclusion: deleting `cordum-rest.yaml` and `cordum.swagger.json` will not break
current Python/TypeScript SDK generation in this repo.

## Planned deletion delta (based on current audit)

1. Simplify `tools/scripts/gen_openapi.sh` so `make openapi` becomes a
   canonical-spec validation flow instead of regenerating `cordum.swagger.json`.
2. Delete `docs/api/openapi/cordum-rest.yaml`.
3. Delete `docs/api/openapi/cordum.swagger.json`.
4. Remove the remaining stale `legacy and prefixed MCP HTTP endpoints` copy
   from published Swagger mirrors (the root canonical spec was cleaned in step 3).
5. Remove MCP dual-registration from `handlers_mcp.go` (the real alias owner).
6. Update non-versioned docs and published Swagger assets to point only at
   `cordum-api.yaml`.
7. Leave `versioned_docs/` alone in this task.

## Verification commands used for this audit

```bash
rg -n "cordum-rest\.yaml|cordum\.swagger\.json|legacy and prefixed MCP HTTP endpoints" \
  cordum/docs cordum/docs-site Cordum-site/docs-site \
  -g '!**/node_modules/**' -g '!**/versioned_docs/**'

rg -n '"/mcp/|/api/v1/mcp/' core/controlplane/gateway/gateway.go
Get-Content core/controlplane/gateway/handlers_mcp.go -TotalCount 140
Get-Content tools/scripts/gen_openapi.sh -Raw
Select-String -Path docs/api/openapi/cordum-api.yaml -Pattern 'deprecated'
rg -n "incident|Incident|dataset|/api/v1/evals" docs/api/openapi/cordum-api.yaml
rg -n "cordum-api\.yaml" sdk/python sdk/typescript sdk/conformance docs
```
