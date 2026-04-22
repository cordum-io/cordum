# Changelog

## Unreleased

- No unreleased changes yet.

## 0.1.0 - 2026-04-20

- Bootstrapped the `cordum-sdk` Python package and vendored the generated
  OpenAPI client from `docs/api/openapi/cordum-api.yaml`
- Added sync + async ergonomic facades with auth helpers, retry/backoff,
  namespace adapters, pagination helpers, streaming/SSE support, and typed
  error mapping
- Added unit + integration coverage for CRUD flows, auth refresh, retry
  behavior, streaming, examples, and build verification
- Added PyPI release workflow and release-operator documentation
