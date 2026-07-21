# CAP-PRODUCTION operator guide

CAP-PRODUCTION is an explicit runtime security profile, not a CAP conformance
tier. The default remains `compat`. A process selected for production refuses
to start unless every boundary that it claims is ready; it never silently
downgrades to compatibility.

## Current component status

| Component | Current status |
|---|---|
| Scheduler | Implements the complete Cordum CAP-PRODUCTION boundary described below. |
| API gateway | Not production-ready. Selecting `CORDUM_CAP_PROFILE=production` fails startup because exact-wire admission, replay, signing, resolver and safety dependencies are not installed in this binary. |
| Workflow engine | Not production-ready. It consumes scheduler-accepted results, but it does not install the complete shared readiness set. Selecting `production` fails startup. |

Do not set one fleet-wide `CORDUM_CAP_PROFILE=production` value today. Activate
the scheduler per process and leave the gateway and workflow engine on
`compat`. A process may advertise the `cap_production` capability only after
its startup gate and subscriptions have completed.

The current API gateway also does not production-sign its outbound submit
packets. A production scheduler therefore rejects ordinary gateway submissions.
Scheduler activation today is limited to isolated integration/canary paths
whose producers already carry an authenticated session and CAP-PRODUCTION
signature; it is not a supported whole-platform cutover.

CAP Go, Python and Node SDKs implement production packet verification,
identity/dispatch echo, sealing and the common signing vectors. Cordum's
no-skip scheduler/worker transport harness currently exercises a Go managed
worker; it is not a claim of full-stack Python or Node transport coverage.

## Scheduler activation

Set the security posture explicitly:

```text
CORDUM_CAP_PROFILE=production
CORDUM_ENV=production
CORDUM_SDK_HANDSHAKE=enforce
CORDUM_HEARTBEAT_MODE=telemetry
WORKER_ATTESTATION=off
OUTPUT_POLICY_ENABLED=true
POLICY_CHECK_FAIL_MODE=closed
OUTPUT_POLICY_FAIL_MODE=closed
```

`CORDUM_ENV=production` makes the NATS constructor reject plaintext and missing
authentication early. CAP-PRODUCTION independently checks the connection's
actual posture before advertising or subscribing: it requires a `tls://` URL,
server-certificate verification, and either a client certificate or NATS
username/password, token, or NKey authentication. Configure `NATS_TLS_CA` and,
for mutual TLS, `NATS_TLS_CERT` plus `NATS_TLS_KEY`.
Do not use `CORDUM_NATS_ALLOW_PLAINTEXT`, `CORDUM_NATS_ALLOW_NOAUTH`, or
`NATS_TLS_INSECURE` in this profile.

Provision both trust authorities documented in
[the authenticated worker handshake guide](sdk/handshake.md):

```text
CORDUM_HANDSHAKE_SCHEDULER_ID=cordum-scheduler
CORDUM_HANDSHAKE_SCHEDULER_KEY_ID=scheduler-proof-v1
CORDUM_HANDSHAKE_PRIVATE_KEY_FILE=/run/secrets/scheduler-proof-private.pem
CORDUM_HANDSHAKE_PUBLIC_KEY_FILE=/etc/cordum/trust/scheduler-proof-public.pem

CORDUM_POLICY_SIGNING_KEY_ID=session_v1
CORDUM_POLICY_SIGNING_KEY_PATH=/run/secrets/session-signing-private.pem
CORDUM_POLICY_PUBLIC_KEY_SESSION_V1=<matching Ed25519 public key>
```

Redis and the Safety Kernel must be reachable through `REDIS_URL` and
`SAFETY_KERNEL_ADDR`. Worker records must carry the canonical tenant, worker,
agent, P-256 proof key ID/public key and allowed topics. Readiness is evaluated
before any scheduler subscription or background worker starts.

### Readiness diagnostics

The final error lists every missing dependency in this stable order:

| Readiness name | Meaning and remediation |
|---|---|
| `authenticated_transport` | The established NATS configuration lacks verified TLS, broker credentials, or a client certificate. Plaintext, `NATS_TLS_INSECURE`, and anonymous TLS cannot activate this profile. |
| `handshake_enforced` | `CORDUM_SDK_HANDSHAKE` is not `enforce`, or its authority bundle is incomplete. |
| `raw_admission_installed` | The exact received-wire verifier was not installed and frozen on the NATS bus. Inspect the earlier `CAP-PRODUCTION transport boundary unavailable` error. |
| `replay_store` | The two-second Redis replay-store probe failed. Restore Redis rather than bypassing replay defense. |
| `trust_store` | Authenticated worker credential/key resolution was not installed. Repair the credential/config service. |
| `session_resolver` | Active session-token authority could not be bound to raw packet admission. |
| `outbound_signer` | The scheduler P-256 key/key ID could not create or freeze the subject-bound encoder. |
| `resource_allowlist` | The sealed `cordum-redis` resolver registry was not constructed. |
| `safety_kernel` | The Safety Kernel client is unavailable. |
| `output_safety` | `OUTPUT_POLICY_ENABLED` is false or the output checker failed to initialize. |
| `fail_closed_modes` | Input or output safety was configured open. Use `closed`; production ignores tenant fail-open overrides. |

`CAP profile configuration invalid` means the profile token is not exactly
`compat` or `production`. `CAP-PRODUCTION engine validation failed` identifies
an engine invariant such as missing safety, disabled identity enforcement, a
non-enforcing handshake, missing output safety, or fail-open mode. The process
exits in every case.

## Signed packets, replay and retry attempts

The raw boundary verifies the exact received bytes before protobuf dispatch.
The signing preimage is versioned and binds the 16-byte message ID, actual
NATS subject as audience, expiry and key ID. Tenant/sender/key resolution is
derived from the authenticated session, not packet claims. Unknown keys,
expired packets, wrong subjects, malformed wire and identity mismatches are
rejected before handlers run.

Replay records bind `(tenant, audience, sender, message ID)` to the exact
unsigned-body digest until signature expiry plus clock skew:

- identical bytes are an idempotent duplicate;
- the same message ID with another digest is a conflict and fails closed;
- replay-store errors are retryable and cause no safety or business side
  effect.

Every physical dispatch receives an unpredictable `dispatch_id`, a monotonic
`attempt`, and the authenticated assigned worker. Result, progress and worker
cancel events must echo the current identity and dispatch exactly. A late
event from an older attempt, the wrong worker or the wrong tenant changes no
state. Privileged control-plane cancellation is a separate authenticated
all-attempt operation.

Accepted results are committed atomically to one Redis Cluster hash:

```text
job:{<base64url(job_id)>}:runtime
```

The hash contains the current fence, state, signed message ID/digest and a
durable outbox effect. State commits before external projection; the outbox is
acknowledged only after legacy projection, saga handling and the trusted
`sys.internal.job.result.accepted` publish complete. A crash may redeliver NATS
traffic, but the logical state/effect is applied once. See the
[job-event migration note](operations/cap-production-job-event-migration.md)
for rolling-upgrade and legacy-key behavior.

## Structured resources

CAP-PRODUCTION does not accept an arbitrary URI fetcher. The scheduler installs
one immutable resolver:

| Setting | Current value |
|---|---|
| Resolver ID | `cordum-redis` |
| URI | `redis://resources/cap:resource:<tenant>:<job_id>:<resource_id>` |
| Redis client | The scheduler's configured `REDIS_URL`; credentials never come from the reference. |
| Maximum declared bytes | 2 MiB |
| Media types | `application/json`, `application/octet-stream`, `text/plain` |

`ResourceRef` must also carry an exact 32-byte SHA-256 digest, nonzero size,
canonical media type, purpose and future expiry. Tenant and job namespace come
from authenticated local authority. Userinfo, query, fragment, percent escapes,
backslashes, traversal, unknown resolver IDs, other tenant/job namespaces,
expiry, type/size mismatch and digest mismatch all fail closed. HTTP, file and
generic Redis resolvers are not installed, so there is no redirect, DNS,
symlink or fallback path to configure.

Compatibility mode retains explicit legacy pointer readers for rolling
migration. Production removes those readers; send the structured reference and
store bytes at the allowlisted key before switching.

## Key rotation

Scheduler key rotation supports an overlap at the worker trust map:

1. Add the next scheduler public P-256 key ID to every worker while retaining
   the current public key.
2. Atomically deploy the matching scheduler private/public pair and new
   `CORDUM_HANDSHAKE_SCHEDULER_KEY_ID`; the scheduler verifies the pair at boot.
3. Confirm new handshakes and signed dispatches, then remove the retired public
   key from workers after old packet/session lifetimes have elapsed.

Worker proof credentials currently store one active P-256 key, not an overlap
set. Replacing the record immediately makes the prior key unresolvable. Drain
that worker, install its next private key, replace the canonical credential,
revoke the old active session, and re-authenticate before returning it to the
pool. A revoked record cannot be reactivated without a fresh proof key.

Rotate the Ed25519 session/service-token key by distributing the next public
trust entry before switching the private key/key ID, then retain the old public
entry through the bounded token lifetime. Never log private keys, session
tokens, signatures, packet bytes or resolver errors containing credentials.

## Compatibility-to-production checklist

1. Upgrade workers and verify handshake sessions in `warn`; keep the overall
   CAP profile `compat`.
2. Replace legacy pointers with valid structured references and remove every
   unsigned or incomplete identity producer.
3. Watch `cordum_bus_compatibility_total{reason=...}`. The bounded reasons are
   `unsigned`, `missing_signature_metadata`, `missing_identity`,
   `legacy_pointer`, and `configured_fail_open`. The scheduler/gateway/workflow
   startup warning also states that compatibility surfaces remain reachable.
4. Reach zero new compatibility observations for the migration window, enable
   output safety, set fail modes closed, and complete key distribution.
5. Activate a scheduler only in an isolated canary with production-signed
   producers. Treat any startup refusal as a failed rollout; do not use an
   override or fall back silently.
6. Keep the gateway and workflow engine on `compat` until their readiness
   implementations land. Their current refusal is intentional and must not be
   presented as full-stack CAP-PRODUCTION activation.
