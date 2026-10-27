# MCP Outbound Signing (ECDSA P-256)

`cordum-mcp`, the standalone MCP bridge, signs the requests it sends
to the Cordum gateway with an ECDSA P-256 signature. Tool calls that
POST or PUT are signed, and so are resource reads. Tool calls that GET,
such as listing jobs, and the boot-time agent lookup go unsigned. The
gateway loads no signing key and signs nothing.

The gateway does not check these headers on the requests it receives:
a signed request is checked only when someone posts it to the
[verification endpoint](#verification-endpoint). The feature is
opt-in. With no signing key, `cordum-mcp` logs a warning at startup
and sends its calls unsigned; a key it cannot parse stops it at
startup.

## Threat model

| Threat | Without signing | With signing |
|---|---|---|
| MITM injects fake tool call | Accepted as real | Signature mismatch → rejected |
| Attacker replays captured request | Accepted every time | Nonce in replay window → rejected |
| Compromised worker spoofs a different agent | Accepted | Signed agent_id must match peer's trust record |
| Clock-skew DoS (far-future timestamp) | n/a | Rejected as expired (5 min default) |

Signing does NOT protect against: (1) a compromised `cordum-mcp`
private key — rotate via `cordumctl mcp keygen` + trust store update;
(2) receivers that don't verify — the headers are advisory to them.

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

`cordum-mcp` puts the HTTP method and path in `method`, for example
`POST /api/v1/jobs`, and the request body in `params`.

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

- **Private key** — deploy to the `cordum-mcp` process only; never
  commit. Stored in `CORDUM_MCP_OUTBOUND_SIGNING_KEY` (inline) or
  `CORDUM_MCP_OUTBOUND_SIGNING_KEY_PATH` (file).
- **Public key** — distribute to every verifier. The gateway's
  verification endpoint reads it from
  `CORDUM_MCP_INBOUND_TRUSTED_KEY_<ID>=<base64>`. The `<ID>` must match
  `cordum-mcp`'s `CORDUM_MCP_OUTBOUND_SIGNING_KEY_ID` (default:
  `default`).

## Environment variables

### Signing process (`cordum-mcp`)

| Variable | Purpose | Required |
|---|---|---|
| `CORDUM_MCP_OUTBOUND_SIGNING_KEY` | PEM or base64 DER of ECDSA P-256 private key | One of KEY / KEY_PATH |
| `CORDUM_MCP_OUTBOUND_SIGNING_KEY_PATH` | File with the same contents as KEY | One of KEY / KEY_PATH |
| `CORDUM_MCP_OUTBOUND_SIGNING_KEY_ID` | Key id stamped in headers | No (default: `default`) |

### Verifier (gateway)

| Variable | Purpose | Required |
|---|---|---|
| `CORDUM_MCP_INBOUND_TRUSTED_KEY_<ID>` | base64 SPKI or PEM of a `cordum-mcp` public key | Yes, one per signing key |

## Clock-skew tuning

Default window: ±5 minutes, checked by the verifier. When the clocks
of the `cordum-mcp` host and the gateway drift further apart than
that, verification fails with `expired`. Sync clocks via NTP;
emergency widening lives in task plan as a future knob.

## Interop with non-Cordum MCP servers

`cordum-mcp` sends signed requests only to the Cordum gateway, so
other MCP servers never see them. The `X-Cordum-*` headers are
additive: a receiver that does not verify, the gateway included,
handles a signed request like an unsigned one, so turning signing on
breaks no caller.

## Verification endpoint

To check a signed request without shipping your own ECDSA code, post
it to the gateway:

```
POST /api/v1/mcp/verify-signature
{
  "method": "POST /api/v1/jobs",
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
