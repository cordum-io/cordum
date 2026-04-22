# MCP Outbound Signing (ECDSA P-256)

Cordum's gateway signs every outbound MCP call with an ECDSA P-256
signature. Cooperating peers verify the signature; non-cooperating
servers see additive headers and ignore them. The feature is
opt-in — no signing key means no signature, no broken requests.

## Threat model

| Threat | Without signing | With signing |
|---|---|---|
| MITM injects fake tool call | Accepted as real | Signature mismatch → rejected |
| Attacker replays captured request | Accepted every time | Nonce in replay window → rejected |
| Compromised worker spoofs a different agent | Accepted | Signed agent_id must match peer's trust record |
| Clock-skew DoS (far-future timestamp) | n/a | Rejected as expired (5 min default) |

Signing does NOT protect against: (1) a compromised gateway private
key — rotate via `cordumctl mcp keygen` + trust store update; (2)
peers that don't verify — the headers are advisory to them.

## Wire format

Every signed outbound request carries these headers:

```
X-Cordum-Key-Id:    prod-2026-04         # which pubkey verifies
X-Cordum-Timestamp: 1776549600           # Unix seconds, UTC
X-Cordum-Nonce:     a1b2c3d4e5f6…        # 128-bit random hex
X-Cordum-Tenant:    default              # caller's tenant
X-Cordum-Agent-Id:  agent-alpha          # calling agent identity
X-Cordum-Signature: base64(DER-ECDSA)    # signed canonical message
```

The **canonical message** the signer hashes is:

```
method
sha256(canonical(params))
nonce
timestamp
tenant
agent_id
```

`canonical(params)` decodes the JSON body and re-encodes it with
key-sorted / whitespace-stripped output so `{"a":1,"b":2}` and
`{"b":2,"a":1}` hash identically.

## Key generation

```bash
# Writes PKCS#8 PEM to priv.pem (0600) and base64 SPKI to stdout.
cordumctl mcp keygen --out priv.pem > pub.b64
cat pub.b64
# ZjBQxJUlxQ... (base64-encoded SubjectPublicKeyInfo)
```

- **Private key** — deploy to the signing gateway only; never commit.
  Stored in `CORDUM_MCP_OUTBOUND_SIGNING_KEY` (inline) or
  `CORDUM_MCP_OUTBOUND_SIGNING_KEY_PATH` (file).
- **Public key** — distribute to every verifying peer; each peer
  registers it via `CORDUM_MCP_INBOUND_TRUSTED_KEY_<ID>=<base64>`.
  The `<ID>` must match the gateway's `CORDUM_MCP_OUTBOUND_SIGNING_KEY_ID`
  (default: `default`).

## Environment variables

### Signing gateway

| Variable | Purpose | Required |
|---|---|---|
| `CORDUM_MCP_OUTBOUND_SIGNING_KEY` | PEM or base64 DER of ECDSA P-256 private key | One of KEY / KEY_PATH |
| `CORDUM_MCP_OUTBOUND_SIGNING_KEY_PATH` | File with the same contents as KEY | One of KEY / KEY_PATH |
| `CORDUM_MCP_OUTBOUND_SIGNING_KEY_ID` | Key id stamped in headers | No (default: `default`) |

### Verifying peer

| Variable | Purpose | Required |
|---|---|---|
| `CORDUM_MCP_INBOUND_TRUSTED_KEY_<ID>` | base64 SPKI or PEM of peer's public key | Yes per peer |

## Clock-skew tuning

Default window: ±5 minutes. Both sides enforce the window on verify,
so a peer with a drifted clock will reject the gateway's calls. Sync
clocks via NTP; emergency widening lives in task plan as a future knob.

## Interop with non-Cordum MCP servers

Other MCP implementations see `X-Cordum-*` as opaque custom headers
and ignore them. The tools/call succeeds normally; only Cordum-aware
peers can verify. Deploying the gateway against a mixed peer set is
safe — peers that don't implement the verifier are not broken.

## Verification endpoint

For peers that want to verify without shipping their own ECDSA code,
Cordum exposes:

```
POST /api/v1/mcp/verify-signature
{
  "method": "tools/call",
  "params": { ... },
  "headers": { "X-Cordum-Key-Id": "...", ... }
}
```

Response:

```
200 OK
{
  "ok": false,
  "sub_reason": "expired",
  "key_id": "prod-2026-04",
  "tenant": "default",
  "agent_id": "agent-alpha"
}
```

`sub_reason` is one of: `missing`, `malformed`, `expired`, `replayed`,
`untrusted_key`, `bad_signature`, `error`. The endpoint is
admin-gated; requires the trust store configured on the gateway.

## Inbound verification for multi-cluster

Single-cluster demos don't need inbound verification — the gateway
does not accept external MCP calls. Multi-cluster setups SHOULD run
the full inbound-verify path as a follow-up integration (Verifier is
already available in `core/mcp/outbound`; wiring into the gateway's
MCP dispatch path is the remaining work).
