# Delegation tokens

Cordum delegation tokens let one Enterprise agent identity delegate a reduced,
time-bounded scope to another agent identity. The gateway issues Ed25519-signed
JWTs, verifies them on `POST /api/v1/jobs`, injects only reserved delegation
labels into the Safety Kernel request, and records lineage in the audit trail.

## Threat model

Delegation tokens are designed to reduce, not expand, an agent's effective
authority.

- **Signer compromise:** if the active Ed25519 private key is leaked, an attacker
  can mint arbitrary delegation JWTs until operators rotate keys and revoke
  affected JTIs.
- **Replay:** tokens are bearer credentials. Keep TTLs short, prefer a
  `parent_token` only when chaining is necessary, and revoke exposed JTIs.
- **Scope escalation attempts:** issuance and verification enforce scope
  monotonicity against both the delegating agent identity and any parent token.
- **Tenant crossover:** issuance rejects cross-tenant targets before signing.
- **Chain abuse:** verification rejects chains deeper than
  `CORDUM_DELEGATION_MAX_DEPTH` (default `3`).
- **Policy bypass attempts:** the raw JWT is never forwarded to the Safety
  Kernel. Only `_delegation.*` labels derived from a verified token are passed
  downstream.

## JWT model

Delegation tokens use standard JWT claims plus Cordum-specific scope fields.

- **Algorithm:** `EdDSA` (`Ed25519`)
- **Issuer:** `cordum`
- **Subject (`sub`):** delegating agent id
- **Audience (`aud`):** target agent id
- **Token id (`jti`):** unique per issuance, used for revocation
- **Registered time claims:** `iat`, `nbf`, `exp`
- **Cordum claims:** `tenant`, `allowed_actions`, `allowed_topics`,
  `delegation_chain`, `chain_depth`, `parent_token_jti`

## Configuration

Required environment variables:

- `CORDUM_DELEGATION_PRIVATE_KEY` — PEM-encoded Ed25519 private key for signing
- `CORDUM_DELEGATION_KEY_ID` — active JWT header `kid`
- `CORDUM_DELEGATION_PUBLIC_KEY_<KID>` — base64 Ed25519 public key(s) accepted
  for verification

Optional environment variables:

- `CORDUM_DELEGATION_MAX_DEPTH` — maximum delegation chain depth (default `3`)
- `CORDUM_DELEGATION_POLICY_ENABLED` — when true, Safety Kernel policy rules can
  evaluate delegation context reconstructed from `_delegation.*` labels

## Rotation procedure

Cordum reuses the same Ed25519 operational model used elsewhere in
`core/licensing`: publish new verification material first, then cut over the
active signer.

1. Generate a new Ed25519 keypair and choose a new `kid` (for example `dlg-2`).
2. Distribute the new public key to every gateway instance as
   `CORDUM_DELEGATION_PUBLIC_KEY_DLG_2=<base64>`.
3. Keep the existing public key env vars in place so already-issued tokens still
   verify during the overlap window.
4. Roll the private signer by updating:
   - `CORDUM_DELEGATION_PRIVATE_KEY`
   - `CORDUM_DELEGATION_KEY_ID=dlg-2`
5. Restart or redeploy gateway instances.
6. Verify the new key is active by issuing a fresh token and checking the
   returned `kid`.
7. After the longest possible TTL plus audit-retention comfort window, remove
   the old `CORDUM_DELEGATION_PUBLIC_KEY_<OLD_KID>` env var.

If you suspect compromise, revoke affected JTIs immediately and rotate without
waiting for natural expiry.

## Curl recipes

All examples assume `X-Tenant-ID: default` and an API key or bearer token with
the required RBAC permissions.

### Issue

```bash
curl -sS -X POST http://localhost:8081/api/v1/agents/agent-a/delegate \
  -H 'X-API-Key: YOUR_API_KEY' \
  -H 'X-Tenant-ID: default' \
  -H 'Content-Type: application/json' \
  -d '{
    "target_agent_id": "agent-b",
    "allowed_actions": ["read"],
    "allowed_topics": ["job.finance.approvals"],
    "ttl_seconds": 900
  }'
```

Successful response:

```json
{
  "token": "eyJhbGciOiJFZERTQSIsImtpZCI6ImRsZy0yIiwidHlwIjoiSldUIn0...",
  "kid": "dlg-2",
  "expires_at": "2026-04-20T08:05:00Z",
  "chain_depth": 1,
  "jti": "a7f3b7f3..."
}
```

### Verify

```bash
curl -sS -X POST http://localhost:8081/api/v1/agents/verify-delegation \
  -H 'X-API-Key: YOUR_API_KEY' \
  -H 'X-Tenant-ID: default' \
  -H 'Content-Type: application/json' \
  -d '{
    "token": "eyJhbGciOiJFZERTQSIsImtpZCI6ImRsZy0yIiwidHlwIjoiSldUIn0...",
    "expected_audience": "agent-b"
  }'
```

Invalid tokens still return HTTP 200:

```json
{
  "valid": false,
  "error_code": "scope_exceeded"
}
```

### Revoke

```bash
curl -sS -X POST http://localhost:8081/api/v1/agents/revoke-delegation \
  -H 'X-API-Key: YOUR_API_KEY' \
  -H 'X-Tenant-ID: default' \
  -H 'Content-Type: application/json' \
  -d '{
    "jti": "a7f3b7f3...",
    "reason": "worker laptop lost"
  }' -i
```

Expected response: `204 No Content`

## Safety policy examples

When `CORDUM_DELEGATION_POLICY_ENABLED=true`, the gateway injects verified
delegation labels and the Safety Kernel can evaluate them using
`match.predicate`.

```yaml
- id: deny-deep-delegation
  match:
    topics: ["job.finance.*"]
    predicate: "delegation.depth > 2"
  decision: deny
  reason: "Only two delegation hops are allowed for finance jobs"

- id: require-approval-for-root-issued-write
  match:
    topics: ["job.prod.*"]
    predicate: "delegation.issuer == 'cordum'"
  decision: require_approval
  reason: "Root-issued delegation into production requires approval"

- id: read-only-delegation-for-reports
  match:
    topics: ["job.reports.*"]
    predicate: "delegation.scope.contains('read')"
  decision: allow
  reason: "Read-only reporting delegation is allowed"
```

Reserved labels injected by the gateway:

- `_delegation.depth`
- `_delegation.issuer`
- `_delegation.issuer_chain`
- `_delegation.scope`
- `_delegation.subject`

## Operator runbook

### A token verifies but job submission is denied

1. Verify the token again with `expected_audience` set to the resolved target
   agent id.
2. Confirm the target worker credential is linked to the same agent identity.
3. Check the agent identity still has the delegated action/topic in its current
   `allowed_tools` / `allowed_topics`. Verification rejects drifted scope as
   `scope_exceeded`.
4. If policy gating is enabled, inspect the job's `_delegation.*` labels and
   the matched Safety Kernel rule.

### Suspected token leak

1. Revoke the exposed `jti`.
2. Audit for `delegation.issue`, `delegation.verify`, and `delegation.revoke`
   events that reference the same lineage.
3. Rotate the signing key if compromise might include signer material.
4. Re-issue only the minimum scopes required by downstream workers.

### Verification returns `unknown_kid`

The gateway does not have the matching `CORDUM_DELEGATION_PUBLIC_KEY_<KID>`
value loaded. Publish the missing public key before retrying verification.

### Verification returns `audience_mismatch`

The token was minted for a different target agent identity than the one
currently receiving the request. Re-issue the token for the correct audience.
