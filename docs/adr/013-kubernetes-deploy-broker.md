# ADR-013: Kubernetes Deploy Broker

- Status: Proposed
- Date: 2026-11-17
- Scope: M2B-01, one broker and one protected operation; not platform identity
- Tags: security-contract, Kubernetes, broker

## Context

The [M0-02 contract](../contract/protected-operation.md) defines manifest apply
to `reference-prod-cluster` / `reference-protected-app`. This ADR is a planned
design only. Execution, isolation, alternate-path exclusion and recovery are
**not measured**; document checks prove none of them.

Source baseline: `bf2afc41bd5820edfb0ce58e6cc90468f8a1aec0`. The
[Helm chart](../../cordum-helm/values.yaml) is scaffolding, not this profile.
A cluster administrator and an endpoint administrator are outside the declared
boundary, as are compromised trusted control-plane components or broker code.
Agents, repository scripts, plugins and agent-controlled CI are untrusted.

## Decision

Choose a target-local broker: projected service-account token, mTLS, RBAC plus
identity-bound admission, conditional SSA updates and create-only POST.
No shell, arbitrary HTTP proxy, kube-context or Helm rendering.

[ADR-011](./011-execution-grant-format.md) owns grant bytes, signatures,
identities and schema evolution. Consume M2A-05 for epoch/revocation freshness,
M2A-06 for durable claim/execution state, M2A-07 for immutable digest lookup,
and M2A-09 for issuance/authenticated retrieval. No broker-local authority,
second grant format or bearer shortcut; missing dependencies refuse activation.

## Placement

M2B-03 must build `cordum-deploy-broker` at proposed `cmd/cordum-deploy-broker`.
Run one replica in target namespace `reference-deploy-broker`; Cordum services
stay in `reference-cordum-management`, applications in `reference-protected-app`,
and agents/CI outside these trusted namespaces.
This replaces M0-02's illustrative separate management cluster; separation
by namespace, node placement, credential and network must be qualified.

Use operator-owned dedicated management nodes, non-root broker, read-only root,
no host namespaces/host mounts/privilege and no untrusted sidecars. Mount only
its projected token, caller-channel TLS key and distinct receipt signing key.
Agents and applications must have no exec, attach, pod creation, scheduling,
secret access or deployment-edit authority in management/broker namespaces.
Default-deny ingress/egress must precede workload admission; allow only the
authority channel, named target API, bounded DNS and required trusted state/input
services. Application egress must exclude target API, management and metadata.
Network reachability is defense in depth, not a substitute for target auth.

Rejected: an agent sidecar or shared worker process, because its files, process
and workload-control surface would expose target credentials to untrusted code.

## Target credential

M2B-04 must use ServiceAccount `reference-deploy-broker/cordum-deploy-broker`
with explicit projected, pod-bound TokenRequest
credentials for the target API audience. Request 3600-second expiry; honor the
issued lifetime/rotating file. Disable automount; project into the broker only.
Do not grant the broker `serviceaccounts/token`, secrets, impersonate, bind or
escalate permissions. Token provisioning/rotation belongs to trusted Kubernetes
infrastructure, not CI ([credentials](https://kubernetes.io/docs/concepts/security/service-accounts/)).

Bind a Role `cordum-deploy-broker` in `reference-protected-app` to that exact
service-account namespace/name. Permit only `get`, `create`, `patch` on core
`services`, `configmaps` and apps `deployments`. Restrict get/patch by the
operator-approved names; create needs a separate rule and admission name checks
because [RBAC cannot name-restrict top-level creates](https://kubernetes.io/docs/reference/access-authn-authz/rbac/#referring-to-resources).
No wildcards, list/watch, delete, update, subresources, RBAC or cluster-wide mutation.
Grant only GET of namespace `reference-protected-app` via a separate,
resourceNames-restricted ClusterRole for namespace-UID binding; no other cluster
resource permission. An independent observer gets separate read-only credentials.

Bind operator-configured cluster ID, HTTPS endpoint/server name/CA and namespace
UID/name to the grant target. Refuse recreation/substitution, user kubeconfig,
exec/auth plugins, proxies, redirects, insecure TLS, impersonation or expired tokens.

Rejected: an admin kubeconfig or long-lived service-account Secret, because its
scope/lifetime and copyable delivery broaden the broker's authority and exposure.

## Caller authentication

M2B-03 must accept only a typed execution-ID request from the M2A-09 authority
service over internal HTTPS/mTLS, then fetch its immutable envelope and trusted
request context there. Agents submit proposals to the gateway; API keys and
agent-supplied grant bytes must not authenticate a broker caller.

Choose operator-issued X.509 certificates from a dedicated service CA, TLS 1.3,
verified clientAuth/serverAuth usages and exact DNS SAN allowlists: authority
`cordum-authority.reference-cordum-management.svc`, broker
`cordum-deploy-broker.reference-deploy-broker.svc`. No Common Name fallback,
wildcard identity or arbitrary CA-valid client. Use distinct keys per service,
mounted read-only; provision replacement before expiry, bound connections to
certificate expiry and refuse new work on invalid/expired identity. Reuse Go
TLS and [chain checks](../../core/infra/tlsutil/chain.go), not a new identity provider.

Bind ADR-011 executor_id to broker DNS identity/kind BROKER and allowed tenants/
targets. Check tenant/principal/agent/session/action against authenticated issuance,
not merely grant/header values. No agent-bus grants or TLS/grant key reuse.

Rejected: shared API keys or bearer grants at the broker, because possession
would conflate proposal identity, executor identity and effect authority.

## Namespace restriction

M2B-08 must implement RBAC **plus** `admissionregistration.k8s.io/v1`
ValidatingAdmissionPolicy and bindings, `failurePolicy: Fail`, action `Deny`.
Match the literal namespace, not an agent-editable label selector; cover all
served equivalent versions. For create/update of protected Deployments,
Services and ConfigMaps require request.userInfo.username equal to
`system:serviceaccount:reference-deploy-broker:cordum-deploy-broker` and the
operator-owned name/kind allowlist. Deny deletes of those protected objects.
Use [authenticated identity](https://kubernetes.io/docs/reference/access-authn-authz/validating-admission-policy/), not annotations, fieldManager or User-Agent.

One narrow controller exception is required: the trusted deployment controller
may update only `deployment.kubernetes.io/revision` metadata while preserving
spec, remaining annotations and labels. Pin its separate service-account
identity with controller-manager service-account credentials enabled; never
allow the entire system:serviceaccounts group. Status and derived ReplicaSet,
Pod and EndpointSlice operations remain separately RBAC-scoped to trusted
controllers; they must not offer an agent-writable deployment route. HPA and
other spec-writing controllers are excluded from this profile.

Broker validation and admission must reject privileged/host access, hostPath,
secret/projected-token/CSI/PVC mounts, secret env/imagePullSecrets, unapproved
images, alternate service accounts and injection annotations. Require the
operator-created `reference-app` service account with no permissions, explicit
`automountServiceAccountToken: false`, Restricted Pod Security, immutable image
digests and only emptyDir/non-secret approved ConfigMap volumes. Admission must
validate both Deployment templates and resulting Pods, including init and
ephemeral containers. Services must be internal ClusterIP with no externalName,
externalIPs, NodePort or LoadBalancer. No custom mutating webhooks, arbitrary
ownerReferences, finalizers or unrecognized workload fields in this profile.
Unknown restrictions deny; valid Kubernetes syntax alone is insufficient.

Operators must protect admission/RBAC, namespaces, identities, nodes and network
policy from agents/CI/applications and inventory indirect credential access.
Admission does not verify grants or withstand compromised brokers/administrators.

Rejected: RBAC alone, because it cannot restrict create by name or validate
workload semantics, and another accidentally granted writer would bypass it.

## Apply semantics

M2B-05 must consume only M2A-07 digest-addressed bytes, check size and SHA-256,
then strictly decode bounded documents. Reject duplicate keys/objects, unknown
fields, List wrappers, generateName, omitted namespace and any object-set
mismatch. Only the three M0-02 kinds and exact approved names are eligible.
No rendering, URL fetch, path reopening or namespace/input substitution.

Choose field manager `cordum-deploy-broker`, `force=false` and strict field
validation. Use server-side apply PATCH (`application/apply-patch+yaml`) only
for existing objects; ownership conflicts are failures, never force takeover.
Approval must cover removal of omitted owned fields, per
[SSA ownership](https://kubernetes.io/docs/reference/using-api/server-side-apply/).

For each object require exactly one of these grant-bound modes:
- Create: MUST_NOT_EXIST, no UID/resourceVersion in the immutable manifest.
  Use POST create with the same fieldManager, not SSA upsert. AlreadyExists
  fails; never convert it to update. This supplies atomic absence enforcement.
- Update: MUST_EXIST plus VERSION_EQUALS with the approved resourceVersion;
  immutable manifest metadata must contain that version and the expected UID.
  Require both to equal a fresh GET, and retain both in the SSA request so a
  race fails at storage. No GET-then-unconditional-PATCH and no refreshed-version
  retry. Missing/deleted/recreated objects must fail, never become creations.

This is broker interpretation of ADR-011 preconditions, not new grant fields.
Use each object's exact Kubernetes API path as its precondition resource key:
`/apis/apps/v1/namespaces/<namespace>/deployments/<name>` or
`/api/v1/namespaces/<namespace>/{services,configmaps}/<name>`, with no aliases,
escapes or query. UID is covered by the approved manifest digest. The API server's
[patch handling](https://github.com/kubernetes/kubernetes/blob/v1.35.0/staging/src/k8s.io/apiserver/pkg/endpoints/handlers/patch.go)
and [storage checks](https://github.com/kubernetes/kubernetes/blob/v1.35.0/staging/src/k8s.io/apiserver/pkg/registry/generic/registry/store.go)
are design inputs; M2B-05 must prove stale-version/delete-recreate races produce
no write, or refuse the mode without weakening its precondition.

After grant verification, dry-run every object with `dryRun=All`, the selected
method and identical options before any live write. Validate defaulted results;
do not replay dry-run output as input. Dry-run is not a reservation or receipt.
[API dry-run semantics](https://kubernetes.io/docs/reference/using-api/api-concepts/#dry-run)
require side-effect-safe admission; absence of that capability must deny.
Then claim via M2A-06 and persist execution intent before the first mutation.
Process objects in ADR-011 sorted order, sequentially, with fresh M2A-05
authority/expiry checks and target/precondition checks immediately before each
write. Apply the M2A-05 freshness bound; none is invented or claimed here.
Cap each API request at 10 seconds and the operation at the lesser of 60 seconds
or remaining grant lifetime. Disable automatic mutation retries, including 429
and transport retry paths. Never refresh a grant's inputs or preconditions.

Per-object results: NOT_ATTEMPTED, CONFIRMED_APPLIED, CONFIRMED_REJECTED,
OUTCOME_UNKNOWN. API success confirms apply, not rollout health; known rejection
confirms that request's failure. Timeout/disconnect/crash/uncertain response is
unknown. Stop on rejection/unknown, preserving prior effects. Success requires
all objects confirmed; partial failure lists applied/untouched objects, and any
uncertain object makes execution unknown. No atomicity, rollback, prune or replay.

Rejected: SSA upsert for MUST_NOT_EXIST and GET-only preconditions are racy.
Rejected: kubectl/client-side apply expands authority via context, rendering and retries.

## Receipt

M2B-06 must store an immutable receipt containing version, receipt ID, execution
and attempt IDs, grant ID/digest, action commitment, tenant/requester/agent/session,
approval reference (when required), executor identity/version/image digest,
policy digest/authority epoch and last-check time, input digest/size, target
cluster/namespace/namespace UID, start/end times and overall outcome.
Per object record GVK/name/namespace, approved UID/version or absence condition,
method/fieldManager, attempted flag, result class, returned UID/resourceVersion,
API status/reason and audit correlation ID when available. Include hashes and
restricted references to saved responses/readback evidence, not credentials,
raw manifests or arbitrary server error bodies. Distinguish absent evidence.

Use [core/policysign](../../core/policysign/policysign.go) Sign/Verify with a
dedicated broker Ed25519 receipt key and separate verifier trust store, never
the grant issuer, TLS, policy or audit key. Sign domain-prefixed exact stored
payload bytes (`CORDUM-K8S-DEPLOY-RECEIPT-V1` plus NUL). Choose UTF-8 JSON from
fixed Go structs: no maps/floats, Unix-millisecond times, version 1, hex digests, ordered
object arrays and explicit null for absent evidence. Envelope: base64 payload
plus policysign.Signature. Verify preserved bytes before strict decoding, never
reconstructed JSON. M2B-06 must pin vectors and reject duplicate/unknown fields
and unsupported versions; this is neither a grant format nor portable canonicalization.
Persist receipt with the M2A-06 terminal transition before reporting completion;
failure to persist leaves a recoverable unknown, not a replayable grant.

Rejected: JobID-only results/success booleans omit inputs and partial effects.
Signatures prove attribution/integrity, not honesty, persistence or health.

## Reconciliation

M2B-07 must load durable execution and immutable grant/input/receipt, then use
uncached named GETs with read-only credentials. Compare UID, resourceVersion,
approved fields and ownership to saved responses/pre-write state. Join independent
audit by request, object, broker and time window; desired-state equality is not causality.

Resolve unknown only with attributable commit evidence and matching readback.
Later change/deletion, UID mismatch, missing audit, unavailable target or ambiguous
response means unknown/drift, not success. Absence cannot exclude transient effects.
Historical apply evidence is not current health. Reconciliation must not resume
writes or reissue authority: subsequent writes need fresh M2A authorization,
current state/preconditions and approval as required.

Rejected: retrying on timeout or treating matching YAML as an idempotency proof,
because either can conceal an earlier commit or attribute another actor's work.

## End-to-end environment

M2B-10 must use disposable Linux/amd64 kind v0.31.0 with node image
`kindest/node:v1.35.0@sha256:452d707d4862f52530247495d180205e029056831160e22870e37e3f6c1ac31f`
from the [kind release](https://github.com/kubernetes-sigs/kind/releases/tag/v0.31.0).
This test baseline is not a production patch recommendation. Use the M0-02
chart profile and separation above; replace kind's default CNI with
[Calico v3.31.0](https://github.com/projectcalico/calico/blob/v3.31.0/manifests/calico.yaml)
for default-deny NetworkPolicy. M2B-10 must lock manifest/image digests, Docker,
kernel, chart/configuration, broker/issuer artifacts and corpus in its evidence;
untested CNI/version compatibility must refuse qualification, not fall back.

Capture audit to an operator-only sink and independent before/during/after state.
Exercise AP01-AP05 and M0-02 SC01-SC06, including
lost responses, partial applies, stale UID/version, revocation and a substituted
second cluster/namespace. Require an approved benign create and update positive
control on the same reachable target. Correlate negative attempts with zero
unauthorized mutations, including delayed/transient effects; denial codes alone
are insufficient. Missing observations are inconclusive. No cluster is run here.

Rejected: fake clients/default networking do not measure storage/admission/isolation.

## Alternate paths

All mechanisms are planned, not measured; administrators remain outside the boundary.

| ID | Alternate path from M0-02 | Planned mechanism and section |
| --- | --- | --- |
| AP01 | Direct kubectl using agent-reachable credentials | No agent target credential; broker-only admission even with another writer; credential inventory and named Role ([Target credential](#target-credential), [Namespace restriction](#namespace-restriction)). Exposure fails qualification even without a write. |
| AP02 | Another SDK, HTTP client, script or plugin | Client-independent RBAC and authenticated-identity admission, not executable names ([Namespace restriction](#namespace-restriction)); no generic broker proxy ([Caller authentication](#caller-authentication)). |
| AP03 | Another service account or workload identity | No token/secret/identity acquisition; restricted workloads and broker identity admission ([Target credential](#target-credential), [Namespace restriction](#namespace-restriction)). |
| AP04 | Agent-controlled CI with a deployment token | CI is proposal-only; exclude target tokens and indirect operator jobs, isolate trusted custody ([Placement](#placement), [Target credential](#target-credential)). |
| AP05 | Changed context, endpoint or namespace | Operator-pinned endpoint/CA/namespace UID, grant equality and explicit request namespace; no kubeconfig inheritance ([Target credential](#target-credential), [Apply semantics](#apply-semantics)). Observe intended and substituted targets. |

## Dependencies

The [go.mod](../../go.mod) pin `k8s.io/client-go v0.35.10`, matching `k8s.io/api`
and `k8s.io/apimachinery`, suffices for REST, token transport and typed objects.
Reuse these, Go TLS, protobuf and core/policysign: no new Go module, kubectl,
Helm execution library, controller-runtime or identity framework. Calico/kind
are qualification infrastructure, not broker library dependencies.

## Open questions

- PR1-06 (`task-835b3e3f`) may supersede this broker's service CA/SAN mapping,
  certificate issuance/rotation and projected-token custody with established
  platform workload identity. It must preserve tenant/executor/target binding,
  separate keys, no agent credentials and requalify the boundary before use.
- M2A-05 must publish freshness/revocation bounds; this ADR does not redefine
  that authority-state contract or measure its bound.
- M2B-06 must freeze receipt field names/vectors; M2B-07 must qualify audit retention
  and reconciliation windows. No missing evidence may become verified success.
- M2B-08/M2B-10 must prove controller exceptions, workload admission and selected
  CNI compatibility with pinned artifacts. Failure blocks activation, not the
  chosen RBAC-plus-admission requirement. Production/HA qualification is separate.
