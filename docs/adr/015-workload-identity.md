# ADR-015: Workload Identity

- Status: Proposed
- Date: 2026-11-26
- Scope: PR1-06, workload authentication design; not human identity
- Tags: workload-identity, security-contract, broker, runner

## Context

Brokers, trusted runner supervisors and control-plane services need identities
that untrusted jobs cannot choose or acquire. Authentication identifies a peer;
it does not authorize an effect. Grants, tenant checks, target permissions and
runtime isolation remain independent requirements.

This ADR is a decision proposal only. It implements nothing and establishes no
supported, enforced or qualified workload-identity profile. Rotation, revocation
latency and credential isolation are **not measured**. Implementation and
qualification rows must follow this decision before activation.

Source baseline: `11ad07536e31f5841500bdb6def39be190fba06e`.
[ADR-011](./011-execution-grant-format.md) defines the G9 executor binding;
[ADR-012](./012-runner-isolation-stack.md) separates trusted supervision from
arbitrary job code. [ADR-013](./013-kubernetes-deploy-broker.md) chooses a
target-local Kubernetes broker, projected tokens and a DNS-identified mTLS
channel. This ADR adopts those choices without superseding them and supplies
the internal certificate lifetime/rotation policy.

Existing building blocks, not evidence of the proposed profile:

- [tlsutil](../../core/infra/tlsutil/chain.go) provides `VerifyChain` for
  certificate/CA and key-usage diagnostics. Its structural check uses the
  certificate's start time; it is not live peer-name or revocation validation.
- [tlsreload](../../core/infra/tlsreload/reloader.go) reloads certificate/key
  files for new TLS connections. It neither issues certificates nor interrupts
  existing connections or rotates CA trust pools.
- [servicetoken](../../core/auth/servicetoken/servicetoken.go) mints reserved
  control-plane identities with a five-minute default lifetime. These stateless
  message tokens have no active-session record for individual early revocation.
- [workercredentials](../../core/controlplane/workercredentials/service.go)
  stores Argon2id token hashes, worker/tenant restrictions and revocation state.
  Its credential record has no expiry field. Worker enrollment/attestation is
  not, by itself, proof of a trusted broker or runner supervisor.

## Options

The operating-cost judgments below are design assessments, not benchmarks.
Lifetimes for alternatives are possible profiles, not vendor defaults or
claims about current Cordum configuration.

### A. Existing mTLS and Cordum credentials

Reuse operator PKI, Go TLS, the helpers above and existing message/worker
credentials where their current protocols require them. Certificate issuance,
identity enrollment and trust distribution remain operator responsibilities.

| Relationship | Trust bootstrap | Lifetime and rotation | Revocation | Operating cost / limitation |
| --- | --- | --- | --- | --- |
| Broker to target | Target must trust the issuing CA and map its client certificate to a target principal; Cordum tokens are not target credentials. | Could use one-hour certificates with renewal at half-life; target must accept replacements. | Stop issuance and remove target permissions; certificate expiry alone is not immediate revocation. | Reuses PKI, but requires target-specific CA/RBAC integration; inferior to the native projected token for ADR-013. |
| Executor to control plane | Operator enrolls the trusted supervisor/broker and pins a dedicated CA plus exact DNS SAN/role mapping. Worker credentials remain separate, not an enrollment shortcut. | Choose one-hour certificates, renew by 30 minutes with new keys; replace files and bound connections. | Disable identity authorization and issuance, terminate channels; leaf expiry is a backstop, not proof of prompt revocation. | Lowest new infrastructure for the initial fixed fleet; automated issuer integration and identity policy are still required. |
| Service to service | Dedicated service CA and per-service identities; existing signing trust for reserved bus-message subjects. | One-hour TLS certificates; existing service tokens last five minutes and are minted for sends, not prolonged. | Disable peer authorization; stateless tokens can remain valid until expiry unless signing trust is removed. | Reuses current transports; separate message-token and TLS trust lifecycles, no universal identity plane. |

### B. SPIFFE with SPIRE

[SPIFFE](https://spiffe.io/docs/latest/spiffe-about/overview/) supplies standard
workload IDs and identity documents, not Cordum authorization.
[SPIRE](https://spiffe.io/docs/latest/spire-about/spire-concepts/) adds a server,
node agents, node/workload attestation and registration policy. Its local
Workload API and host isolation become trusted infrastructure.
[X.509-SVIDs](https://spiffe.io/docs/latest/spiffe-about/spiffe-concepts/) permit
mutual authentication with rotating short-lived credentials; prefer them to
replayable bearer JWT-SVIDs where the transport permits.

| Relationship | Trust bootstrap | Lifetime and rotation | Revocation | Operating cost / limitation |
| --- | --- | --- | --- | --- |
| Broker to target | Target must accept the SPIFFE trust domain and authorize the exact SPIFFE ID, or use a separately configured exchange. | Could choose one-hour X.509-SVIDs and consume Workload API updates before expiry; an exchanged target token has its own lifetime. | Stop issuance/remove authorization; already-issued credentials require expiry or an explicit target-side deny mechanism. | Adds SPIRE plus target integration; a URI SAN alone does not satisfy ADR-013's Kubernetes service-account admission identity. |
| Executor to control plane | Attest enrolled nodes and trusted supervisor processes, distribute pinned bundles and registration selectors. | Could choose one-hour X.509-SVIDs with automatic renewal; clients must consume updates and reconnect. | Remove registration/authorization and terminate channels; do not assume registration deletion invalidates every outstanding SVID. | Strong fit for a heterogeneous fleet, but introduces agents, server availability, selector security and a G9 mapping migration. |
| Service to service | Register each service identity and explicitly configure trust-domain/federation boundaries. | Short-lived SVIDs and bundle updates via the Workload API; a one-hour profile would need qualification. | Peer authorization and expiry; emergency trust-bundle changes affect the whole relevant trust domain. | Consistent identity across platforms, at the cost of operating SPIRE and integrating every transport. |

### C. Platform workload identity

Use [Kubernetes service accounts](https://kubernetes.io/docs/concepts/security/service-accounts/)
for the Kubernetes API, or a target's established cloud workload federation.
For example, [AWS IRSA](https://docs.aws.amazon.com/eks/latest/userguide/iam-roles-for-service-accounts.html)
exchanges an audience-bound projected token for temporary IAM credentials using
an operator-configured OIDC trust relationship. That is not a generic Cordum
peer identity; provider policies, SDK refresh and metadata isolation still matter.

| Relationship | Trust bootstrap | Lifetime and rotation | Revocation | Operating cost / limitation |
| --- | --- | --- | --- | --- |
| Broker to target | Kubernetes issuer, dedicated service account, pinned API CA and narrowly bound RBAC/admission; cloud targets require their own federation policy. | Choose a 3600-second projected-token request for Kubernetes; kubelet rotates, broker reloads. Cloud credentials have provider-issued expiries and SDK renewal. | Kubernetes bound-object validation and RBAC changes; cloud session revocation/expiry is provider-specific. Rotation does not revoke a copied credential. | Lowest incremental infrastructure for a target-local broker; couples each target to its native identity and authorization model. |
| Executor to control plane | Control plane would trust explicit cluster issuers/audiences and bind service-account/object identities, or cloud federation subjects. | Could request 3600-second audience-specific tokens; refresh from the platform, never silently use a node role. | TokenReview can check bound-object state; offline JWT verification cannot observe deletion until expiry. Cloud behavior varies. | Convenient for Kubernetes-only execution, but adds validation connectivity and does not cover ADR-012 bare-host supervisors uniformly. |
| Service to service | Each receiver must validate a dedicated audience/issuer and authorize the exact platform subject; server TLS is still required. | Projected-token refresh or provider SDK refresh before the issued expiry. | Online platform validation or expiry; cached/offline validation needs an explicit staleness bound. | Little issuer infrastructure inside one platform; bearer-token handling, cross-platform mappings and bus-message identity remain work. |

## Decision

Choose **C for broker to target**, **A for executor to control plane**, and
**A for service to service** in the initial managed profile. Do not add SPIRE
for this first fixed deployment. Its portability is valuable, but does not
justify a new operational dependency before cross-platform attestation is
required. Native target authentication avoids a second target identity mapping;
mTLS retains ADR-013's channel contract and works for trusted Linux supervisors.

No new identity standard is introduced. Compose existing X.509/TLS, Kubernetes
TokenRequest and existing Cordum protocols; do not invent an identity issuer,
token format, cryptographic scheme or replacement enterprise identity platform.
No automatic SPIFFE/cloud fallback or alternative bearer login is selected.

### Broker to target: native Kubernetes identity

Adopt ADR-013's exact service account
`reference-deploy-broker/cordum-deploy-broker`, pod-bound projected token,
target-API audience and pinned endpoint/server name/CA. Request **3600 seconds**;
the actual issued expiry is authoritative, not an assumption that a request
forces the server's lifetime. Qualification must record the issued lifetime
and reject a profile whose expiry exceeds its approved exposure bound.

The kubelet handles [projected-token rotation](https://kubernetes.io/docs/tasks/configure-pod-container/configure-service-account/#serviceaccount-token-volume-projection);
the broker must reload the rotating file for subsequent requests and refuse
expired credentials. No long-lived Secret token, agent token copy, ambient
kubeconfig, node credentials or credential-broker fallback is allowed.
The target authenticates the service account, not G9's DNS name; their mapping
belongs to operator-owned broker deployment configuration, not grant input.

Retain ADR-013 RBAC/admission and agent isolation. Removing target permission
or the bound workload is an incident-response measure, not a measured zero-delay
revocation guarantee. Cloud targets remain unselected until a target-specific
ADR fixes issuer/audience, permissions, session lifetime and revocation behavior.

### Executor to control plane: dedicated-CA mTLS

Use ADR-013 TLS 1.3, dedicated service CA, distinct private keys and exact DNS
SAN allowlists with clientAuth/serverAuth checks. Retain its authority and
broker DNS identities. Enroll runner supervisors separately with unique
operator-assigned DNS identities; a job's sandbox never receives the supervisor
key, a Workload API socket, target credentials or an enrollment credential.

Leaf certificates must have validity **at most one hour**, with automated
renewal and a fresh key **by half-life (30 minutes for a one-hour leaf)**.
Use the operator's existing PKI issuance/distribution system; no Cordum CA
service is proposed. Install replacements before expiry and validate at the
actual current time. Missing, expired, not-yet-valid, wrong-role or mismatched
peer identity refuses new work; there is no insecure development fallback in
this profile. The issuer outage budget is the remaining credential lifetime.

Reuse tlsreload's callbacks, but implement and qualify the missing connection
and trust-pool lifecycle: cap connection authentication age at five minutes
and certificate expiry, whichever is earlier; reauthenticate after renewal.
Resumption must not bypass fresh identity/authorization checks. Do not confuse
loading a certificate successfully with authenticating a live peer.

### Service to service: same mTLS profile, existing message tokens

Use the same **one-hour maximum / half-life renewal** and connection bounds,
with distinct service keys and explicit caller-to-method authorization. Pin
trust per deployment/security domain; a CA-valid peer is not automatically an
authorized caller. Server-name verification is required as well as client
authentication. Do not share a wildcard service identity across components.

Retain existing service tokens only for their reserved bus-message purpose:
**five minutes**, newly minted for sends via the existing minting path. Never
extend an issued token or use it as a broker credential or G9 identity proof.
The current shared signing trust is a control-plane trust boundary, not
cryptographic isolation between individual services. It stays separate from
TLS, grant and receipt keys. Worker credentials retain their existing worker
protocol role; this ADR does not relabel them short-lived executor credentials.

### Trust rotation and revocation obligations

Bootstrap CA roots, endpoint names, enrollment and allowed tenant/target/role
mappings through operator-controlled configuration, never agent-supplied data.
For routine CA rotation, distribute new trust first, switch issuance, renew all
leaves, then remove old trust after the last old leaf expires and old channels
are drained. Root expiry is a hard deadline, not an allowed outage bypass.

On compromise, disable issuance and the affected identity's authorization,
terminate its channels and rotate affected keys/trust. Do not wait for routine
overlap on a compromised CA. These enforcement hooks need implementation;
neither tlsutil nor tlsreload proves revocation. Stateless service tokens may
survive until their five-minute expiry unless verifier trust is removed.

Certificate/token expiry bounds credential validity, not grant freshness.
ADR-011's authority-epoch and revocation checks still apply before an effect;
an authenticated connection is not a cached permission to execute. Follow-up
rows must define and measure identity-disable propagation and fail closed when
the required authorization state is unavailable. No immediate-revocation SLA
is claimed here, and an in-flight target mutation cannot be undone by revocation.

### Intended executor: ADR-011 G9 binding

`executor.executor_id` binds to the **exact operator-enrolled DNS SAN identity
of the trusted executing broker or runner supervisor**, authenticated through
the dedicated-CA mTLS profile. It is not the requesting agent, worker ID, IP,
pod name, service-account token subject, certificate serial or a caller header.
Issuance selects that identity from trusted configuration, and authenticated
retrieval/channel context must agree; the executor compares the grant to its
own enrolled identity. No identity is learned from the grant itself.

For ADR-013 the exact value remains
`cordum-deploy-broker.reference-deploy-broker.svc`, with
`executor.executor_kind = BROKER`. A registered trusted supervisor uses its
assigned DNS identity with `executor.executor_kind = RUNNER`. Kind comes from
the protected enrollment, not a peer's assertion. `UNSPECIFIED`, unknown kind,
wrong trust domain, ambiguous SAN mapping, name mismatch or wrong kind denies.
No Common Name fallback, wildcard match, alias or case normalization is allowed.

Key renewal retains the enrolled identity; identity reassignment to a different
trust boundary requires invalidating old authority before issuing new grants.
Bind each identity to allowed tenants, execution profile and targets separately;
DNS equality alone is insufficient. The broker's service identity does not
select a particular replica: retain ADR-013's one replica initially, and require
ADR-011 durable single-use claims before any future replica expansion. Rotating
a key never resets grant consumption. Untrusted job code is not the executor.

## Consequences

- Reuses existing standards and code, but still requires secure enrollment,
  automated PKI integration, exact peer authorization, trust reload and channel
  expiry handling. A deployment lacking these is outside this proposed profile.
- Keeps target credentials inside the target-local broker and runner credentials
  outside sandboxes; identity alone proves neither isolation nor safe behavior.
- Avoids a SPIRE dependency now; heterogeneous attestation/federation later needs
  an explicit migration ADR, including G9 mappings and existing-grant handling.
- Follow-up implementation rows must cover issuance/rotation failures, stale
  trust, copied tokens, wrong SAN/kind/tenant, issuer outage and reconnect paths.
  Qualification needs reachable positive controls and proof that denied attempts
  caused no protected mutation, process launch or credential disclosure.
  No such runtime evidence is produced by this documentation change.

## Open questions

- Which existing operator PKI product and enrollment proof will be qualified
  for the one-hour profile on Kubernetes and managed runner hosts? No product
  or deployment is approved by this record.
- What identity-disable propagation bound and outage policy will the follow-up
  rows prove, including existing channels and service-token trust removal?
- What actual projected-token lifetime does the pinned target issue, and what
  incident-response latency can its RBAC/bound-object checks demonstrate?
- If fleet scale requires SPIRE, which attestors and trust domains are needed,
  and how will migration preserve G9, tenant separation and single-use authority?
