# Authenticated worker handshake and session trust

This document is the operator and SDK contract for worker identity. The trust
boundary is the CAP protobuf challenge/authenticate protocol backed by enrolled
P-256 proof keys and Redis session state. A NATS connection, a self-reported
worker ID, a heartbeat, or the legacy capability `Handshake` is not identity
proof.

## Trust model

The authenticated flow binds all of the following before Cordum mints a
session:

- tenant, worker ID, and agent identity;
- the enrolled worker proof-key ID and its ECDSA P-256 public key;
- protocol version, request/trace IDs, client and server nonces, and timestamps;
- the fixed audience `cordum-scheduler`;
- the expected scheduler ID and its pinned ECDSA P-256 signing key;
- the worker's capability `Handshake`, SDK version, and ready topics.

Cordum uses two deliberately separate key families:

| Authority | Algorithm | Private key holder | Purpose |
|---|---|---|---|
| Worker proof | ECDSA P-256/SHA-256 | worker | Proves possession of the key enrolled on the worker credential. |
| Scheduler proof | ECDSA P-256/SHA-256 | scheduler | Lets the worker authenticate the challenge before signing it. |
| Session/control-plane signing | Ed25519 | scheduler, gateway, workflow engine | Signs short-lived worker sessions and internal service tokens. |

Do not reuse a private key across these roles. The worker never sends its
private key. Cordum never returns a session token until both signatures and all
bindings verify.

## One protobuf contract

The only session-minting protocol is CAP `BusPacket` request/reply on core NATS:

1. `WorkerHandshakeChallengeRequest` ->
   `sys.worker.handshake.challenge`.
2. Scheduler returns a signed `WorkerHandshakeChallenge` containing a fresh,
   single-use server nonce.
3. Worker verifies the scheduler signature and every echoed field, then sends
   `WorkerHandshakeAuthenticate` ->
   `sys.worker.handshake.authenticate`. The authenticate packet signs the
   complete challenge and the nested capability `Handshake`.
4. Scheduler atomically consumes the Redis challenge, verifies the enrolled
   worker proof key and authoritative agent/tenant/worker link, then returns a
   signed `WorkerHandshakeResult`.

Renewal uses the same two subjects and protobuf messages with purpose `RENEW`.
It also requires the current active bound session token. There is no separate
JSON handshake, renew subject, or unsigned compatibility mint path.

The generic CAP capability `Handshake` remains useful for version,
capabilities, and ready-topic telemetry. On its legacy standalone subject it is
self-asserted and cannot create trust. Inside `WorkerHandshakeAuthenticate` it
is trusted only because it is covered by the worker proof and bound to the
accepted session. Cordum registers no responder for `sys.worker.handshake`,
`sys.worker.handshake.renew`, or the old generic handshake subject; those
packets receive no session and grant no dispatch eligibility.

## Enroll a worker

1. Create the agent identity in the same tenant.
2. Generate a P-256 worker proof key on the worker host:

   ```bash
   umask 077
   openssl ecparam -name prime256v1 -genkey -noout -out worker-proof.pem
   openssl pkey -in worker-proof.pem -pubout -out worker-proof-public.pem
   ```

3. Issue or rotate the worker credential and link it to the agent. Send the
   public SPKI PEM, never the private key:

   ```json
   {
     "worker_id": "worker-01",
     "agent_id": "agt_01...",
     "proof_key_id": "worker-01-proof-v1",
     "proof_algorithm": "ECDSA_P256_SHA256",
     "proof_public_key_pem": "-----BEGIN PUBLIC KEY-----\n...\n-----END PUBLIC KEY-----",
     "allowed_pools": ["default"],
     "allowed_topics": ["job.example.run"]
   }
   ```

   Submit this body to `POST /api/v1/workers/credentials` with an authorized
   tenant-scoped admin identity. The response intentionally omits the public
   PEM from list/read views. The one-time legacy bearer credential returned by
   this endpoint is not the proof private key and cannot replace the
   authenticated handshake.

4. Configure the SDK with the exact worker/agent/tenant/key IDs, the worker
   private key, audience `cordum-scheduler`, expected scheduler ID, and the
   pinned scheduler public-key map. Enable the SDK trust mode only after the
   values are complete.

Current stable Go, Python, and Node artifacts expose the same contract:

| SDK | Runtime configuration |
|---|---|
| Go | `runtime.Agent.HandshakeMode` plus `capsdk.WorkerTrustConfig` |
| Python | `Agent(worker_trust_mode=..., worker_trust=WorkerTrustConfig(...))` |
| Node | `new Agent({ workerTrust: { mode, config } })` with `createWorkerTrustConfig` |

All three reject unknown modes, partial identity, a non-P-256 key, an unpinned
scheduler, wrong audience, malformed/oversize packets, and altered correlation.
Use each installed package's exported constants rather than spelling subjects,
limits, or protocol values in application code.

## Control-plane keys

Active scheduler mode (`warn` or `enforce`) requires all four P-256 settings:

```text
CORDUM_HANDSHAKE_SCHEDULER_ID=cordum-scheduler
CORDUM_HANDSHAKE_SCHEDULER_KEY_ID=scheduler-proof-v1
CORDUM_HANDSHAKE_PRIVATE_KEY_FILE=/run/secrets/scheduler-proof-private.pem
CORDUM_HANDSHAKE_PUBLIC_KEY_FILE=/etc/cordum/trust/scheduler-proof-public.pem
```

The scheduler verifies at boot that the public SPKI file matches the private
key. Workers must pin that public key under the same key ID.

The scheduler and gateway need a matching Ed25519 signing key and trust entry
whenever worker trust is active. In `enforce`, the workflow engine also needs
the same authority so its internal cancels carry a verifiable service token:

```text
CORDUM_POLICY_SIGNING_KEY_ID=session_v1
CORDUM_POLICY_SIGNING_KEY_PATH=/run/secrets/session-signing-private.pem
CORDUM_POLICY_PUBLIC_KEY_SESSION_V1=<matching Ed25519 public PEM or base64>
```

`CORDUM_POLICY_DEV_SIGNING_SEED` is for local development only. Never use it as
a production key source. The Helm chart's `workerTrust` values accept only
secret references and refuse to render an active mode with an incomplete
P-256 or Ed25519 authority bundle.

## Safe rollout and rollback

Enforce covers the complete governed bus boundary, not only worker sessions:
worker results/progress/heartbeats, authenticated capability/readiness,
session ISSUE/RENEW, and every JobRequest submission. Cordum's gateway,
scheduler retry/saga paths, and workflow engine publishers attach an
audience-bound control-plane service token. External producers must use an
authenticated gateway path, or an explicitly provisioned Cordum control-plane
service identity, before the cluster moves to enforce. An ordinary CAP packet
signature alone is not a JobRequest service credential; tokenless requests are
rejected and are never treated as migration telemetry.

`CORDUM_SDK_HANDSHAKE` and `CORDUM_HEARTBEAT_MODE` are one rollout unit on both
scheduler and gateway. The table is the recommended progression:

| Phase | Handshake | Heartbeat | Effect |
|---|---|---|---|
| Compatibility | `off` | `authority` | No authenticated responder/session authority; legacy heartbeat TTL governs dispatch. Shipped default. |
| Observe migration | `warn` | `warn` | Authenticated sessions govern dispatch; heartbeat is compared and disagreement is emitted. Tokenless heartbeat/capability advertisements are retained only as telemetry: they never refresh liveness, readiness, or the dispatch snapshot. Invalid tokens are rejected. |
| Enforce | `enforce` | `telemetry` | Bound session required for worker traffic and dispatch; heartbeat is telemetry only. Target after the fleet is clean. |

The boot validation accepts two classes: `off` only with `authority`, or either
active handshake mode (`warn`/`enforce`) with either session-authority heartbeat
mode (`warn`/`telemetry`). Therefore `enforce`+`warn` and
`warn`+`telemetry` are valid deliberate configurations even though they are not
the recommended three-phase sequence.

Before the first active phase:

1. Enroll every worker proof key and distribute the scheduler public proof key.
2. Deploy the Ed25519 session key/trust entry to scheduler and gateway; deploy
   it to workflow engine before `enforce`. Confirm in-tree publishers attach
   service authority and external publishers route through the gateway or an
   explicitly provisioned service identity.
3. Set `WORKER_ATTESTATION=off`. The legacy bearer-attestation gate and active
   handshake cannot run together because both interpret `BusPacket.auth_token`
   differently; scheduler boot rejects the combination.
4. Upgrade workers and verify authenticated sessions in a non-production pool.
5. Flip both mode variables together. A contradictory pair or unknown value
   refuses to boot.

Rollback the pair together to `off` + `authority`. An `off` scheduler rejects
configured P-256 handshake settings, so remove those four environment variables
from the process when rolling back. Retain key material in the secret manager;
do not copy it into logs or manifests.

## Session lifecycle

- The accepted session is Ed25519-signed, audience-bound to
  `cordum-scheduler`, and stored as the single active worker/agent/tenant/key
  binding in Redis. Default lifetime is one hour.
- SDKs renew before expiry through a fresh challenge/proof exchange. Renewal
  must present the current active token. Missing, expired, revoked,
  superseded, wrong-audience, or differently bound tokens cannot renew.
- Successful issue or renewal installs a new JTI and supersedes/revokes the old
  token, so an in-flight old token cannot be replayed as a second live session.
- `POST /api/v1/workers/{id}/revoke-session` revokes the active session.
- Revoking or replacing a proof credential removes its future proof and
  dispatch authority; do not treat credential rotation alone as active-session
  revocation. Explicitly revoke the active session (or wait for its expiry)
  while rotating the key, then re-authenticate with the new key ID/public key
  and corresponding private key.

Challenge state and active/revoked/superseded session state are shared in
Redis, so HA replicas preserve the same single-use and single-active-session
rules.

## NATS and logging hardening

Use TLS plus authenticated NATS accounts. At minimum:

- workers may publish only the two handshake request subjects and their job
  result/progress subjects, and may subscribe only to authorized job subjects
  plus their request inboxes;
- schedulers may subscribe to both handshake subjects and publish replies to
  request inboxes; use NATS response permissions/`_INBOX` ACLs rather than a
  broad `>` permission;
- challenge/authenticate use core NATS request/reply, not JetStream. Do not
  persist or replay them.

Never log or export a session token, private key, raw signature/proof, raw
challenge nonce, authorization header, or complete handshake packet. Safe
diagnostics are tenant/worker/agent IDs, bounded request/trace IDs, public key
ID/fingerprint, protocol version, mode, and the stable rejection category.

## Remediation

| Symptom/category | Action |
|---|---|
| Boot rejects mode pair | Use `off` only with `authority`, or `warn`/`enforce` with `warn`/`telemetry`, consistently on scheduler and gateway. |
| Boot reports incomplete P-256 bundle | Supply all scheduler ID/key ID/private-file/public-file settings and confirm the files contain one matching P-256 pair. |
| Session issuer/trust store unavailable | Supply matching Ed25519 private/public material under one key ID and verify Redis connectivity. |
| `unknown_agent` / binding failure | Verify the agent exists in the tenant and the worker credential links that exact agent and worker. |
| Unknown/revoked proof key | Re-enroll the worker with a fresh P-256 public key/key ID; do not restore a revoked key. |
| Wrong audience/scheduler/key ID | Use audience `cordum-scheduler` and the exact pinned scheduler identity/key ID. |
| Replay, expired challenge, or clock skew | Discard the exchange, correct clocks, and start a fresh challenge. Never retry the same authenticate bytes. |
| Missing/altered trace, identity, nonce, version, capability, or signature | Treat as tampering or a mixed-version client; replace the whole exchange and inspect NATS boundaries. |
| Session expired/revoked/superseded | Stop admitting work, obtain a fresh authenticated session, and investigate rotation/revocation audit events. |
| Legacy handshake receives no reply | Expected: legacy subjects cannot mint. Upgrade/configure the authenticated SDK flow. |

For fleet triage, see
[Worker health runbook](../operations/runbook-worker-health.md). For deployment
variables, see [Configuration reference](../configuration-reference.md). The
protocol source of truth is CAP `proto/cordum/agent/v1/handshake.proto` and its
worker-trust specification; this guide does not create a second wire contract.
