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
