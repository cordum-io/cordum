# Policy Rules: Unified GET API

`GET /api/v1/policy/rules` returns the canonical `Rule` envelope shape
across all four rule types (input | output | velocity | edge) and across
all storage sources (interactively-authored RuleStore + YAML pack
bundles). Backend 5d (epic-d9a6c0a1, task-025a6ad9) replaced the legacy
per-row shape that left the dashboard's `/policies` Rules surface
rendering every row's Type column as Unknown.

OpenAPI source: `cordum/docs/api/openapi/cordum-api.yaml` v`2026-05-10.3`.
Companion docs: `policy-rules-write.md` (Backend 5c POST/PUT contract).

## Authorization

Same gate as the rest of the policy read surface:

- `X-Tenant-ID` header.
- `policy.read` permission OR the `admin` role.
- Standard auth (API key / SAML session / mTLS).

## Response shape

```json
{
  "items": [
    {
      "id": "cordclaw.input.aws-secret",
      "name": "Block AWS access keys in prompts",
      "type": "input",
      "scope": {"kind": "global"},
      "status": "published",
      "version": "v1",
      "audit": {
        "created_at": "2026-05-10T12:00:00Z",
        "created_by": "cordclaw",
        "pack_source": {
          "fragment_id": "cordclaw/safety",
          "pack_id": "cordclaw",
          "overlay_name": "safety",
          "tier": "global",
          "version": "1.0.0",
          "installed_at": "2026-05-09T10:00:00Z",
          "sha256": "sha256:..."
        }
      },
      "match": {"topics": ["job.*"], "keywords": ["AKIA"]},
      "decide": {"decision": "deny", "reason": "AWS access key matched"},
      "description": "Cordclaw safety pack — blocks AWS access keys."
    }
  ],
  "has_more": false,
  "next_cursor": ""
}
```

## Source merging

The unified envelope merges four sources:

1. **RuleStore** (`core/policy/rule_store.go`) — interactively-authored
   rules created via `POST /api/v1/policy/rules` (Dashboard 3E
   publish-to-bundle flow). Carries `Type` on the Rule itself; no
   translation needed. `Audit.PackSource` is `omitempty` for these.
2. **YAML input bundles** — parsed via
   `policybundles.RulesFromPolicyContent`. Each bundle's `rules:` array
   becomes Type=`input`. Pack source attribution via
   `policybundles.PolicyRuleSourceFromBundle`.
3. **YAML output bundles** — parsed via
   `policybundles.OutputRulesFromPolicyContent`. Each bundle's
   `output_rules:` array becomes Type=`output`.
4. **Velocity bundles** — bundle id prefix `velocity/`, parsed via
   `velocityRuleFromBundle` in `handlers_velocity.go`. Each becomes
   Type=`velocity`. The legacy `velocityRuleResponse` payload (window,
   key, threshold) embeds under `match.velocity` so downstream
   evaluators see one contiguous match-aware payload.

Edge rules surface only via (1) in this PR — there is no clean YAML
pack bucket for `Type=edge` today. Future enhancement (D11+) may add
a translation path if a stable edge-pack format emerges.

## Translation: legacy YAML map → unified Rule

`core/policy/legacy_to_unified.go::LegacyMapToUnifiedRule` accepts the
YAML-derived `map[string]any` (as `policybundles.RulesFromPolicyContent`
emits it) plus a caller-specified `RuleType` discriminator and an
optional `*PackSource`.

| Legacy field        | Unified field             | Notes                                                    |
|---------------------|---------------------------|----------------------------------------------------------|
| `id` (required)     | `Rule.ID`                 | Missing → `ErrInvalidLegacyRule`.                        |
| `name`              | `Rule.Name`               | Falls back to `id` when omitted.                         |
| (caller-supplied)   | `Rule.Type`               | input \| output \| velocity \| edge.                     |
| `tier` + `selector` | `Rule.Scope`              | tenant scope reads `selector.tenants[0]`; etc.           |
| `status`            | `Rule.Status`             | Defaults to `published` (YAML packs are prod state).     |
|                     | `Rule.Version`            | Server-set `"v1"` for translated rules.                  |
| `match`             | `Rule.Match`              | Lossless JSON. Velocity rules embed `velocity` block here. |
| `decision`+`reason`+`severity` | `Rule.Decide`  | Synthesized.                                             |
| `description`       | `Rule.Description`        | Falls back to `reason` when omitted.                     |
|                     | `Rule.Audit.PackSource`   | Caller-supplied; preserved losslessly.                   |

## Filtering

All filters are AND-combined and applied in-memory after merging
sources. The dataset is bounded (~64 rules across cordclaw + openclaw +
visa + b2b + claude-code packs in the current dev stack).

| Query param   | Behaviour                                                                  |
|---------------|----------------------------------------------------------------------------|
| `type`        | Exact match against `Rule.Type` (input \| output \| velocity \| edge).     |
| `scope_kind`  | Exact match against `Rule.Scope.Kind`.                                     |
| `scope_value` | Exact match against `Rule.Scope.Value` (use with `scope_kind`).            |
| `status`      | Exact match against `Rule.Status` (draft \| published \| deprecated).      |
| `pack_id`     | Match against `Rule.Audit.PackSource.PackID` — useful for filtering by installed pack. |
| `limit`       | Page size, 1..500. Defaults to 100. Out-of-range returns 400.              |
| `cursor`      | Opaque base64 of last-returned `id`. Pass back unmodified for next page.   |

Invalid filter values (e.g. `?type=bogus`) return `400 Bad Request` with
a typed error message.

## Pagination

Cursor encodes the last-returned rule id, base64-no-padding. Sort key
is `id ASC` across the merged set. Stability under concurrent inserts:

- A new rule whose id sorts AFTER the cursor surfaces on the next page.
- A new rule whose id sorts BEFORE the cursor is skipped (acceptable —
  no row is duplicated; the next reload picks it up).

`has_more=true` + non-empty `next_cursor` ⇔ more pages exist.
`has_more=false` + empty `next_cursor` ⇔ end of result set.

## Legacy paths

These two endpoints are kept alive for backwards compatibility. Both
return their pre-5d shape and serve their existing consumers
unchanged. Deletion lands with the Dashboard 11 cut-over PR.

| Endpoint                              | Replacement                                          |
|---------------------------------------|------------------------------------------------------|
| `GET /api/v1/policy/output/rules`     | `GET /api/v1/policy/rules?type=output`               |
| `GET /api/v1/policy/velocity-rules`   | `GET /api/v1/policy/rules?type=velocity`             |

Both are marked `deprecated: true` in the OpenAPI spec.

## Versioning

`info.version` in `cordum-api.yaml` was bumped from `2026-05-10.2` to
`2026-05-10.3` to reflect the response-shape replacement on the
existing path.

## Related work

- Backend 5c — unified Rule write API + add-rule-to-bundle endpoint
  (POST/PUT `/api/v1/policy/rules` + POST `/api/v1/policy/bundles/{id}/rules`).
- Backend 5b — unified GET `/api/v1/policy/decisions` (read + WS
  stream) shipped the same paginated cursor pattern this PR mirrors.
- Dashboard 11 — final cut-over PR that deletes the legacy
  `/api/v1/policy/output/rules` and `/api/v1/policy/velocity-rules`
  paths along with their handlers + tests.
