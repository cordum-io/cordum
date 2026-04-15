---
sidebar_position: 1
title: REST API
slug: /api-reference/rest-api
---

# REST API Reference

The Cordum Gateway exposes a REST API at `http://localhost:8081`.

## Authentication

All protected endpoints require an API key (`X-API-Key` header) or Bearer JWT token.

## Tenant Isolation

Protected routes require the `X-Tenant-ID` header.

## Endpoint Groups

| Group | Base Path | Auth |
|-------|-----------|------|
| Jobs | `/api/v1/jobs` | API key |
| Workers | `/api/v1/workers` | Admin |
| Agent Identities | `/api/v1/agents` | Admin |
| Workflows | `/api/v1/workflows` | API key |
| Policy | `/api/v1/policy` | Admin |
| Approvals | `/api/v1/approvals` | Admin |
| Configuration | `/api/v1/config` | Admin |

For the full endpoint reference, see the [OpenAPI specification](https://github.com/cordum-io/cordum/blob/main/docs/api/openapi/cordum-rest.yaml).
