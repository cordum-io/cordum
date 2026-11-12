# ADR-011: Execution Grant Format

- Status: Proposed
- Date: 2026-11-12

## Context

Roadmap M2 calls for a versioned execution-grant contract. The first protected
operation (M0-02) is applying approved, immutable Kubernetes manifest bytes to
one named cluster and namespace. A policy decision currently has the shape of
[`SafetyDecisionRecord`](../../core/model/safety.go); the signed executor-bound
grant, authority epoch and content-addressed input store proposed here do not
yet exist. This ADR is a planned design, not a supported, enforced or verified
capability. It implements no schema, verifier, broker or protected operation.

Source review: cordum `549772371995b9eb5a95a2c6c8685551e0231724` and CAP
`24677e462d12d0b4afa99723c156c722ae9c5e3d`. M0-02 is cited by row key because
its contract is not on this source baseline.

## Decision

- Use a protobuf `ExecutionGrant` payload and a detached Ed25519 signature in
  a protobuf `SignedExecutionGrant` envelope, bound to one intended executor.
- No new cryptographic scheme is introduced: grants use Ed25519 through the existing core/policysign package (Sign, Verify, TrustStore, ParsePublicKey); domain tags are message framing, not a new primitive.
- M2A-02 will add `core/protocol/proto/v1/execution_grant.proto`, package
  `cordum.v1`, generated into `core/protocol/pb/v1` by `make proto`.
- `core/execgrant` will own canonical bytes, validation, digests and golden
  vectors (M2A-02); `core/execgrant/verify` will own verification and independent
  executor ceilings (M2A-03/M2A-04). These are proposed paths, not existing APIs.

## Envelope options

| Option | Encoding + signature | Canonical bytes | Repository reuse | Main risks | Verdict |
| --- | --- | --- | --- | --- | --- |
| E1 | Protobuf ExecutionGrant + detached Ed25519 in SignedExecutionGrant | Restricted proto3 profile below | core/policysign; existing protobuf toolchain | Deterministic serialization alone is not a portable canonicalization contract | Chosen; pin wire rules and vectors |
| E2 | Compact JWS (RFC 7515), EdDSA (RFC 8037) | Exact JWS signing input; separate JSON canonicalization needed for grant identity | go-jose/v4 is in go.mod, used only in tests | JSON numbers, duplicate keys and unknown members need strict handling | Not selected |
| E3 | Existing delegation-token JWT | Signed header.payload; separate canonical identity needed | core/auth/delegation, golang-jwt/v5, EdDSA | Agent-presented bearer semantics, default unknown-claim tolerance, keyring/type confusion | Not selected |
| E4 | Existing servicetoken JWT-style format | Exact header.claims input | core/auth/servicetoken uses policysign | Local lowercase alg `ed25519`, not the registered JOSE identifier; JSON strictness still needed | Not selected |
| E5 | COSE_Sign1 (RFC 9052) + deterministic CBOR (RFC 8949 section 4.2) | Explicit deterministic CBOR profile | cbor is indirect only; no COSE implementation in this repo | New dependencies and tooling for this contract | Not selected |

Chosen: E1

E1 follows the exact-byte model in [policysign](../../core/policysign/policysign.go)
and CAP-PRODUCTION raw-wire signing. Typed integers and 32-byte digests avoid
JSON precision ambiguity; strict parsing must still reject protobuf ambiguity.
The repository's five sorted-key JSON helpers are not RFC 8785 implementations.
Default JWT unknown-claim tolerance is not the required fail-closed behavior;
sharing delegation's bearer format or keyring also mixes trust domains.
Ed25519 is deterministic, allowing golden vectors to pin signatures as well as
payload bytes. DSSE PAE was considered; the CAP domain-prefix pattern was kept
for consistency, without reusing CAP's ECDSA algorithm or signature domain.

Standards: [JWS](https://www.rfc-editor.org/rfc/rfc7515),
[JOSE EdDSA](https://www.rfc-editor.org/rfc/rfc8037),
[COSE](https://www.rfc-editor.org/rfc/rfc9052),
[CBOR](https://www.rfc-editor.org/rfc/rfc8949).

## Canonical bytes

These are normative requirements for the planned implementation:

1. Grant bytes `B` use proto3 fields in ascending number order, each singular
   field once, minimal varints, and no explicitly encoded default-valued scalar.
   Repeated fields are contiguous, sorted by their named key and unique, except
   `delegation_chain`, which preserves root-to-leaf order. No unknown fields.
   The schema forbids maps, float/double, Any/Struct/Value, groups, proto3
   optional and imported message types (including CAP and well-known types).
   Required message/oneof presence is validated; an empty message cannot stand
   in for missing required content. Identifiers are printable ASCII and compared
   byte-exactly: no trimming, case folding or default identity fallback.
2. The reference encoder is protobuf-go
   `proto.MarshalOptions{Deterministic: true}` at the go.mod version (currently
   v1.36.12), pinned by M2A-02 vectors. Protobuf's
   [determinism is not canonical across runtimes or builds](https://protobuf.dev/programming-guides/serialization-not-canonical/).
   Wire rules and vectors, not a library flag alone, define this profile.
3. The producer validates, serializes once, signs, then stores and sends those
   same bytes. Signature verification uses exact received bytes first; it never
   substitutes a re-serialization. After signature verification, strictly parse
   `B`, reject unknown fields/enums at every depth, then require deterministic
   re-marshaling to equal `B`. Reject duplicates (including multiple oneof
   members), out-of-order fields, explicit defaults and non-minimal varints.
   Validate repeated-field ordering separately; marshaling does not sort lists.
4. Signature input `P = ASCII("CORDUM-EXECUTION-GRANT-SIGNATURE-V1") || 0x00 || B`.
   Use `policysign.Sign(issuerKey, keyID, P)` and store its raw 64 signature bytes.
   Verify with `policysign.Verify(trustedKey, P, sig)`, rebuilding `Signature`
   with Algorithm=`ed25519`, KeyID=envelope key_id, Value=standard base64 of the
   raw signature, Hash=hex SHA-256(P), and SignedBytes=len(P).
5. Grant digest is `SHA-256(B)` with **no prefix**, for durable state (M2A-06),
   approval linkage (M2A-08) and evidence (M2A-10). It is not SHA-256(P).
   CAP spec/19 likewise separates signed-body identity from signing input.
6. Action commitment is `SHA-256(ASCII("CORDUM-EXECUTION-GRANT-ACTION-V1") ||
   0x00 || canonical_action_bytes)`. This fixes the commitment contract;
   M2A-08 will implement it and its approval-record field. The action sub-message
   uses the same canonical profile as the grant.
7. The envelope is `SignedExecutionGrant { bytes grant = 1; string key_id = 2;
   string algorithm = 3; bytes signature = 4; }`. All four fields are required;
   `grant` is `B`, algorithm is exactly `ed25519`, and signature is exactly 64
   raw bytes. Exactly one signature is allowed. Cap total size before parsing;
   reject unknown, duplicate or malformed envelope fields using the same wire
   profile. M2A-02 fixes bounds before implementation can be used.

## Schema location

Choose cordum, not CAP: this is an issuer-to-executor trusted-core contract,
not an agent-bus message. CAP spec/17 requires ignoring unknown fields; grants
must reject unrecognized restrictions. Cordum pins `cap/v2 v2.17.0` with no CAP
replace, so putting this schema there would add a release, dependency bump and
cross-feature gate to schema changes. [ADR-005](./005-output-policy-architecture.md)
already establishes a local control-plane proto precedent.

A later move to CAP for non-Go or out-of-tree executors remains an open question.
Relocation alone need not change signed bytes: proto package names are not wire
data. Field numbers, types, canonical rules, version and domains must remain
unchanged; relocation is not permission to reinterpret existing grants.

## Grant fields

Group legend: **G1** tenant, human principal, agent, session and delegation;
**G2** operation and canonical parameters; **G3** target account, environment,
resource and state preconditions; **G4** inputs/artifacts and tool/runtime;
**G5** effective policy digest and authority epoch; **G6** approval and action
commitment; **G7** enforcement profile; **G8** time, uses and execution identity;
**G9** intended executor/broker. **M** is envelope or payload metadata.

Types below are proposed protobuf types, not implemented symbols. `bytes(32)`
and `bytes(16)` mean bytes with an exact length check. Nested messages are local
to the grant schema. Names such as `tenant_id`, `principal_id`, `sha256`,
`media_type` and `size_bytes` follow CAP IdentityBinding/ResourceRef vocabulary
without importing their messages. M2A-02 assigns payload field numbers.

| Group | Field | Type | Required | Verifier rule |
| --- | --- | --- | --- | --- |
| M | envelope.grant, envelope.signature, envelope.algorithm | bytes, bytes(64), string | Yes | Exact-byte signature and algorithm checks; M2A-03: bad signature. |
| M | envelope.key_id | string | Yes | Only dedicated local trust lookup; M2A-03: unknown key. |
| M | schema_version | uint32 | Yes, 1 | Explicit allowlist; M2A-03: unknown version. |
| M | issuer_id, issuer_key_id | string, string | Yes | Match operator-configured issuer/key binding and envelope key_id; unknown key or action mismatch (M2A-03). |
| G1 | tenant_id, principal_id, agent_id, session_id | string each | Yes | Compare with independently authenticated/request-bound identities, not self-asserted grant values; wrong tenant or action mismatch (M2A-03); absent value: missing required field. |
| G1 | delegation_chain | repeated DelegationLink { string agent_id; string token_jti; string parent_jti; } | If delegated | Root-to-leaf order, unique nonempty token_jti; root parent empty, each later parent_jti equals preceding token_jti. Maximum is the configured delegation depth (default 3); issuer validates provenance, M2A-05 checks revocation of every JTI. Empty only for independently established non-delegated authority. |
| G2 | action.operation | string | Yes | Registered protected-operation id, initially kubernetes.manifest.apply; unknown ids deny (M2A-09 registry; M2A-03 action mismatch). |
| G2 | action.parameters | oneof { KubernetesManifestApply kubernetes_manifest_apply; } | Exactly one | Must match operation; absent: missing required field; different variant: action mismatch (M2A-03). |
| G2 | action.kubernetes_manifest_apply.manifest_input | string | Yes | Names exactly one action.inputs entry; missing reference or altered binding denies (M2A-03 digest mismatch). |
| G2 | action.kubernetes_manifest_apply.objects | repeated KubernetesObjectRef { string api_group; string api_version; string kind; string namespace; string name; } | Nonempty | Closed object set, sorted/unique by (api_group, api_version, kind, namespace, name); api_group may be empty for core APIs; all others required. Namespace equals target.resource; M2B-01 validates manifest-to-object equality and apply/ownership/conflict semantics. |
| G3 | action.target.account, action.target.environment, action.target.resource | string each | Yes | Independently established cluster/account identity, not a kube-context alias; resource is the named namespace. Any mismatch: action mismatch (M2A-03); resolution semantics M2B-01. |
| G3 | action.preconditions | repeated Precondition { string resource; PreconditionKind kind; string expected_version; } | As required by operation | Kind is MUST_EXIST, MUST_NOT_EXIST or VERSION_EQUALS; UNSPECIFIED/unknown denies. Sort by (resource, kind), unique on that key; expected_version required only for VERSION_EQUALS, otherwise empty. Broker evaluates immediately before effect; unsupported or unsatisfied condition denies (M2B-01). |
| G4 | action.inputs | repeated InputDigest { string name; string media_type; bytes sha256; uint64 size_bytes; } | Nonempty | Sort/unique by name; sha256 exactly 32 bytes; size within bounds. Read immutable bytes by digest from M2A-07, check length and re-hash before use; never reopen a path. M2A-03: digest mismatch. |
| G4 | action.runtime | RuntimeIdentity { string tool_id; string tool_version; bytes image_sha256; } | Yes; image digest when profile requires | Bind approved tool/version and, when required, 32-byte image identity to actual runtime; missing field or action mismatch (M2A-03); profile interpretation M2A-04/M2B-01. |
| G5 | policy_digest | bytes(32) | Yes | Identity of effective policy from M1 hardening, not a file label; M2A-09 resolves identity and M2A-03 rejects mismatch. |
| G5 | authority_epoch | uint64 | Yes, greater than 0 | Match authoritative current epoch; older or unknown future value denies. Freshness and revocation semantics owned by M2A-05. |
| G6 | approval.state | ApprovalState enum { UNSPECIFIED, NOT_REQUIRED, APPROVED } | Yes | UNSPECIFIED/unknown denies; NOT_REQUIRED only when independently checked policy and M2A-04 ceilings allow no approval; never an issuer-controlled override. |
| G6 | approval.approval_ref, approval.approver_ids, approval.action_commitment | string; repeated string; bytes(32) | When APPROVED | Nonempty reference and sorted/unique nonempty approvers; M2A-08 resolves the immutable approval record, checks identity/tenant/action, and recomputes commitment from action. Missing: missing required field; mismatch: action mismatch (M2A-03). Must be absent for NOT_REQUIRED. |
| G7 | enforcement_profile.profile_id, enforcement_profile.required_capabilities | string; repeated string | Yes, nonempty | Capabilities sorted/unique bytewise; executor must have every capability. M2A-04 independently requires minimum profile/capabilities per operation; unsupported or downgraded restrictions deny. |
| G8 | grant_id | bytes(16) | Yes | Cryptographically random authorization id; M2A-06 binds it to tenant, execution_id and grant digest, never signature bytes. |
| G8 | execution_id | string | Yes | Durable execution/idempotency identity; M2A-06 rejects a changed grant digest under the same execution and atomically prevents repeated claims, including re-issuance under another grant_id. |
| G8 | issued_at_unix, not_before_unix, expires_at_unix | int64 seconds each | Yes, positive | issued_at <= not_before < expires_at; lifetime expires_at - issued_at within M2A-04 ceiling; bounded skew only as M2A-04 defines. M2A-03: expired or not yet valid. |
| G8 | max_uses | uint32 | Yes, exactly 1 in v1 | M2A-06 atomic single-use claim before effect; state unavailable means deny, not best-effort execution. |
| G9 | executor.executor_id | string | Yes | Equals verifying executor's authenticated identity, not a caller-supplied label; M2A-03: wrong executor. |
| G9 | executor.executor_kind | ExecutorKind enum { UNSPECIFIED, BROKER, RUNNER } | Yes | Known nonzero kind must match authenticated runtime; M2A-03 wrong executor; concrete identity/channel semantics M2B-01. |

All strings, repeated fields, message depth and total bytes require explicit
bounds; M2A-02 fixes the numbers. Set ordering is bytewise lexicographic on the
named string/tuple keys (numeric enum order for kind), with no normalization.
Kubernetes apply mode, ownership/conflict handling and state-precondition
semantics remain M2B-01 decisions; new parameter fields bump schema_version.
The `action` sub-message is the unit of approval, not the mutable submission.
Approval lookup binds the action commitment to identities and tenant separately.

The table covers all ten M2A-03 typed rejection reasons: bad signature, unknown
key, expired, not yet valid, wrong executor, wrong tenant, action mismatch,
digest mismatch, unknown version and missing required field. Detailed error
classification beyond those reasons belongs to that verifier row; no unmatched
failure becomes an allow. A claim is not proof of completion or exactly-once
external effects; outcome uncertainty and reconciliation belong to M2A-06/M2B.
