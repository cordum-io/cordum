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

## Policy Rules

Once the gateway has verified a delegation token, the Safety Kernel projects
the result into a `DelegationContext` that policy rules can match against
via the structured `delegation:` block on `PolicyRule.Match`. Enable the
feature with `CORDUM_DELEGATION_POLICY_ENABLED=true`; when unset the kernel
ignores `_delegation.*` labels, giving operators a rollback lever.

### YAML schema

```yaml
rules:
  - id: example
    decision: allow
    reason: example
    match:
      topics: ["job.sensitive.*"]
      delegation:
        max_depth: 2                # optional; omit to ignore chain depth
        issuers: [agent-a, agent-b] # optional allowlist; every chain member must be listed
        require_issuer: agent-a     # optional; pins the chain's root issuer
        required_scope: [read]      # optional; required ⊆ delegation.Scope
        forbid_delegated: false     # optional; when true, rule fires ONLY on direct calls
```

| Field | Type | Default | Semantics |
|-------|------|---------|-----------|
| `max_depth` | `int` (≥0) | unset | Rule matches when `delegation == nil OR delegation.Depth <= max_depth`. |
| `issuers` | `[]string` | unset | Rule matches when `delegation == nil OR every id in delegation.IssuerChain ∈ issuers`. Strictest interpretation — relax via `require_issuer` for root-only checks. |
| `require_issuer` | `string` | `""` | Rule matches when `delegation == nil OR delegation.RootIssuer == require_issuer` (case-insensitive). |
| `required_scope` | `[]string` | unset | Rule matches when `delegation == nil OR required_scope ⊆ delegation.Scope` (case/whitespace canonicalised). |
| `forbid_delegated` | `bool` | `false` | When `true`, rule matches ONLY when `delegation == nil` (direct-call gate). Short-circuits all other fields. |

Every field is optional; an absent `delegation:` block makes the rule
delegation-neutral. Validation (applied at `ParseSafetyPolicy` time):
`max_depth` must be ≥ 0; issuer ids are unique and match
`^[A-Za-z0-9][A-Za-z0-9_\-\.]{0,127}$`; `required_scope` entries must be
non-blank.

### "No delegation = direct call" (load-bearing rail)

The kernel treats `delegation == nil` as a direct call that passes every
delegation rule except `forbid_delegated: true`. Direct calls already
passed tenant authN + API-key scopes at the gateway, so the delegation
fields are **additive restrictions on chained calls**, not replacements
for normal authZ. Concretely:

```yaml
rules:
  - id: require-finance-root
    decision: allow
    match:
      topics: ["job.finance.*"]
      delegation:
        require_issuer: finance-bot
```

A direct call to `job.finance.reconcile` matches this rule (no chain to
reject), falls through to the decision (allow), and proceeds. A delegated
call rooted at `analyst-bot` does NOT match this rule — it falls through
to the default decision. If the default is `deny`, the chained call is
blocked; if `allow`, it proceeds. Choose the default based on your risk
posture.

To explicitly gate a rule on "direct-only", set `forbid_delegated: true`:

```yaml
rules:
  - id: direct-only-on-sensitive
    decision: allow
    match:
      topics: ["job.sensitive.*"]
      delegation:
        forbid_delegated: true
```

### Worked examples

**1. Forbid any delegation on a sensitive topic**

```yaml
- id: no-chain-on-sensitive
  decision: deny
  reason: chained calls not allowed on sensitive topics
  match:
    topics: ["job.sensitive.*"]
    delegation:
      forbid_delegated: true  # rule fires on direct calls; deny applies
```

Invert the logic if you want to allow only direct calls: set
`decision: allow` and pair with a `deny`-default policy.

**2. Cap chain depth at 2**

```yaml
- id: allow-shallow-chains
  decision: allow
  reason: shallow delegation permitted
  match:
    topics: ["job.sensitive.*"]
    delegation:
      max_depth: 2  # allow-envelope: rule fires only when depth <= 2
```

With `default_decision: deny`, deeper chains fall through and are denied.

**3. Restrict to an issuer allowlist**

```yaml
- id: finance-authority
  decision: allow
  match:
    topics: ["job.finance.write"]
    delegation:
      issuers: [finance-bot, analyst-bot]  # every chain member must be in this list
```

**4. Require a specific root issuer**

```yaml
- id: finance-rooted-only
  decision: allow
  match:
    topics: ["job.finance.*"]
    delegation:
      require_issuer: finance-bot  # chain must be rooted at finance-bot
```

**5. Require minimum scope**

```yaml
- id: require-read-scope
  decision: allow
  match:
    topics: ["job.finance.reconcile"]
    delegation:
      required_scope: [read, write]  # chain's effective scope must ⊇ {read, write}
```

### Operator runbook: "forbid deep chains; only finance-bot may be root"

```yaml
default_decision: deny
rules:
  - id: block-deep-chains
    decision: deny
    reason: delegation chain exceeds depth=1
    match:
      topics: ["*"]
      # MaxDepth=1 makes this rule match chains of depth ≤ 1 AND direct calls.
      # Since decision is deny, it will deny both direct calls and depth≤1 —
      # so we pair it with a more-specific allow rule that covers the intended
      # direct-call traffic ABOVE this rule in the rules list.
      delegation:
        max_depth: 1
  - id: allow-finance-rooted
    decision: allow
    match:
      topics: ["*"]
      delegation:
        max_depth: 1
        require_issuer: finance-bot
```

Rules are evaluated top-to-bottom; place the allow rule above the deny so
that a finance-bot-rooted depth-1 chain is allowed before the catch-all
deny fires. Depth > 1 chains do not match either rule and hit the
`default_decision: deny`.

### Observability

Each rejection emits an increment to the Prometheus counter
`safety_rule_delegation_match_total{field, outcome}`. Expected values:

| `field` | When it fires |
|---------|---------------|
| `forbid_delegated` | A delegated request hit a `forbid_delegated: true` rule. |
| `max_depth` | `delegation.Depth > max_depth`. |
| `issuers` | A chain id is outside the allowlist. |
| `require_issuer` | `delegation.RootIssuer` is not the required value. |
| `required_scope` | The chain's scope is missing a required action. |

Safety-decision audit events emitted downstream include `delegation.depth`,
`delegation.root_issuer`, `delegation.parent_issuer`, `delegation.chain`,
and `delegation.jti` in the SIEMEvent `Extra` map. The full scope list is
deliberately excluded to stay under the 8 KiB syslog line limit; the
decision log carries the full lineage via the policy-decision-log endpoint.

### Policy troubleshooting

| Symptom | Likely cause |
|---------|--------------|
| Direct call unexpectedly denied | A `max_depth`/`issuers`/`require_issuer`/`required_scope` rule is paired with a `deny` decision — nil-delegation "bypass" matches the rule, the deny fires. Flip the rule to `allow` or gate it with a `topics:` filter that excludes the direct-call topic. |
| Delegated call falls through to default | The rule's envelope (max_depth/issuers/require_issuer/required_scope) rejects the chain. Check `safety_rule_delegation_match_total{field=...,outcome="deny"}` to see which sub-field fired. |
| Rule never matches delegated calls | `CORDUM_DELEGATION_POLICY_ENABLED` is unset — the kernel ignores `_delegation.*` labels and treats every request as direct. Set the env var on the safety-kernel process. |
| `ParseSafetyPolicy` rejects a bundle | Invalid agent id in `issuers` or `require_issuer` (must match `^[A-Za-z0-9][A-Za-z0-9_\-\.]{0,127}$`), duplicate issuer entry, negative `max_depth`, or blank entry in `required_scope`. |
| Chain carries correct ids but rule denies | Check the canonicalisation: issuer comparisons are case-insensitive and whitespace-trimmed, scope comparisons are case-insensitive. Rule YAML that relies on case-sensitive matching is a policy bug. |
