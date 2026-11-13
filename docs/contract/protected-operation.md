# First protected operation: Kubernetes manifest apply

Status: **planned / not implemented**. Acceptance evidence: **not measured**.
This is an M0-02 future-state contract, not an installation guide or a claim
that the current release enforces Kubernetes deployment authority.
Reviewed source: `5efc8731ca12551acfc24b984e2f59747f258d2d` (2026-11-11).
All requirements below describe the intended qualified M2 boundary, not
behavior established by this document. Tags: security-contract, Kubernetes, M0.

## Operation

The one protected action is applying approved, immutable Kubernetes manifest
bytes to the fictional cluster **`reference-prod-cluster`**, namespace
**`reference-protected-app`**. These are test-contract identifiers, not live
production coordinates. Approval of a command string is not approval of this
operation. The future executor must use the same immutable input that was
approved, with no post-approval rendering, substitution, or mutable reference
resolution that changes its meaning.

Before any effect, the future authority boundary must establish:

- An authenticated requester and tenant, a separately authenticated human
  approver when required, and current, unexpired, unrevoked execution authority.
  Approval cannot override a hard prohibition.
- The exact input digest, independently identified target cluster and namespace,
  permitted operation, current effective policy, and applicable restrictions.
  Local kube-context names are not sufficient target identity.
- A closed resource-kind set for this reference case: `apps/v1 Deployment`,
  `v1 Service`, and non-secret `v1 ConfigMap`, with explicitly approved object
  names. Unlisted kinds, subresources, namespaces and cluster-scoped objects are
  excluded. Workload images must have approved immutable identities.
- Explicit object preconditions, including expected existence and, for updates,
  object identity/version. Missing, stale, unsupported or mismatched conditions
  must prevent writes, rather than broaden the authorization.
- No workload configuration that conveys management or target authority:
  privileged execution, host access, protected secret mounts, identity changes,
  and access to operator/broker credentials are outside this operation.

Changing the cluster, context, namespace, artifact or restrictions requires a
fresh authorization; it is not a retry of the same approved request. Delete,
exec, arbitrary commands, cluster administration, permission changes and
implicit escalation are excluded. Apply mode, dry-run behavior, conflict
handling, field ownership and precondition mechanics remain M2B-01 decisions;
these requirements do not select a Kubernetes client or apply algorithm.
Kubernetes API processing/defaulting may change the stored representation:
input identity does **not** promise byte-identical YAML in cluster storage.

## Protected resource

The resource is **mutation authority over the declared objects in the selected
namespace**, not the spelling of `kubectl`, a shell, an API URL, or a particular
agent integration. Every agent-originated route to that authority must stay
inside the same declared boundary. Infrastructure operators retain separately
trusted administrative authority; agents may neither borrow it nor invoke it
through agent-controlled automation.

The intended trusted executor is a future deploy broker. Merely returning a
policy decision, installing a hook, or observing a cluster is not that broker.
Target authorization and workload restrictions must also prevent the permitted
objects from reaching more powerful identities or credentials indirectly.

## Alternate paths

These are proposed post-M2 checks, **not measured** and not instructions for
triggering a current defect. Credential-bearing cases describe configurations
to prohibit and exercise with synthetic test identities, never credentials
allowed on the agent side of the reference deployment.

| ID | Alternate path | Expected failure boundary after M2 | Required absence-of-effect observation |
| --- | --- | --- | --- |
| AP01 | Direct `kubectl` using agent-reachable credentials | Provisioning must reject any agent-accessible protected-namespace credential; direct target access must have no mutation authority even if the client can connect. | Independent target audit plus before/after object inventory shows zero unauthorized creates, updates or deletes; credential exposure invalidates the deployment even without a write. |
| AP02 | Another Kubernetes SDK, HTTP client, script or plugin | Client choice must not confer authority; target identity/authorization and the resource boundary must reject agent-originated mutation outside the broker path. | Correlate attempted operations across clients with target audit and state; zero unauthorized mutations, including transient mutations. |
| AP03 | Another service account or workload identity | Agents must not assume a target-capable identity or obtain it indirectly via workload configuration, mounts or privileged execution; target authorization/admission must reject it. | Identity and credential inventory plus target audit/state shows no authority acquisition and zero unauthorized target mutations. |
| AP04 | Agent-controlled CI using a deployment token | CI may propose but must not hold or obtain target deployment authority; provisioning and target authorization must exclude this path. Trusted operator credentials must not be usable by agent-controlled jobs. | CI credential/identity inventory and independently collected target audit/state shows zero unauthorized target mutations; an exposed target-capable token fails qualification. |
| AP05 | Changed kube context, cluster endpoint or namespace | Executor target binding must reject a substituted target rather than inherit caller context; direct attempts must also lack target authority. | Observe both intended and substituted test targets: zero unauthorized mutations on either, with binding rejection correlated to the attempt. |

An HTTP denial alone is insufficient. The later fixture must observe a bounded
test window, including delayed effects, with independent target audit and state
capture; absent/incomplete telemetry means inconclusive, not a pass. It must
also demonstrate an approved benign apply through the protected path on the
same reachable target. An unreachable target or an always-deny executor is not
proof of alternate-path exclusion. No such fixture is run in this task.

## Reference deployment

The reference profile will reuse the existing deployment scaffolding, not
reinterpret it as proof of resource enforcement. Sources at the reviewed SHA:

- [Chart values](../../cordum-helm/values.yaml),
  [control-plane deployments](../../cordum-helm/templates/deployment-control-plane.yaml),
  [service account](../../cordum-helm/templates/serviceaccount.yaml),
  [secrets](../../cordum-helm/templates/secret.yaml), and
  [network policies](../../cordum-helm/templates/networkpolicy.yaml).
- [Base manifests](../../deploy/k8s/base.yaml),
  [production overlay](../../deploy/k8s/production/kustomization.yaml), and
  [TLS patch](../../deploy/k8s/production/patches/tls-env.yaml).
- The [shadow detector](../../core/edge/shadow/k8s/detector.go) has a read-only
  `kubeReader` surface; [CLI shadow discovery](../../cmd/cordumctl/edge_doctor_shadow.go)
  is observation, not manifest apply authority.

### Selected profile and flows

This is a **proposed chart-based qualification profile**, not chart defaults or
a rendered/deployed configuration. Select the gateway, dashboard, Safety Kernel,
scheduler, workflow and context services in a trusted management domain, with
self-managed external Redis (password authentication over TLS) and NATS (mTLS
client authentication). Require authenticated HTTPS, signed policy inputs and
audit authentication; no plaintext/no-auth exceptions. Human local accounts and
Cordum API keys are the selected existing authentication mechanisms. Proposal
scoping and broker authentication still require the M2 ADRs and qualification.
No new authentication protocol or grant schema is defined here.

Illustrative placement is a separate management cluster with namespace
`reference-cordum-management`, including an isolated future broker workload;
agents execute elsewhere and application objects live only in
`reference-protected-app`. Physical placement is **not binding**: M2B-01 owns it
and must demonstrate equivalent separation. The Kustomize production overlay
is a source comparison, not a second profile silently combined with Helm.
Its base explicitly disables service-account token automount; that observation
is not a claim about the Helm service account or a running installation.

| Flow | Owner / trust domain | Intended permission and authority limit |
| --- | --- | --- |
| Agent/repository/plugin/CI -> gateway HTTPS | Agent domain -> Cordum management | Proposals and permitted status reads only; no target credential, approval, policy administration, issuer or broker credential access. |
| Human -> gateway/dashboard HTTPS -> authority state | Human approver/admin -> management | Explicit authenticated approval/admin actions; approval and policy authority never inferred from an agent request. |
| Cordum services -> Redis/NATS; gateway/scheduler -> Safety Kernel; gateway -> context | Platform operator / management | Authenticated state, messaging and evaluation flows (Redis 6379, NATS 4222, kernel 50051, context 50070 in cited scaffolding); these do not grant target apply authority. |
| Authority service <-> future broker | M2A/M2B / trusted management | Authenticate caller, bind approved input/target and refresh current authority before effect; transport, endpoints and credential choice remain ADR questions. |
| Future broker -> named Kubernetes API | M2B / broker -> trusted target | Only current authorized apply within the stated resource/precondition boundary; intended HTTPS target path, not a shell or agent-selected context. |
| Independent observer <- target audit/state | SRE/security / trusted evidence domain | Read-only qualification/reconciliation observations; no execution authority. |
| Agent, application workload or agent-controlled CI -> target/management authority | Denied boundary, owned by platform/security | No direct protected mutation, credential retrieval, identity assumption, or management-secret path; isolate both network reachability and identity/authorization. |

The chart's network-policy template is scaffolding for service flows, **not**
proof of the last row or a broker policy. M2B must specify/test target RBAC,
admission, CNI/network paths, secret isolation and workload restrictions. Trusted
bootstrap/operator actions are separately administered and recorded, not an
agent-invocable exception. No broker is installed by this document.

### Credential inventory

All holders/storage below are the **required prospective allocation**. Existing
mechanisms are source observations only; isolation, provisioning and enforcement
for this profile remain **not measured**. No secret values are included.
Sources: **V/D/S** = chart values/deployments/secrets linked above; **K/T** = base
manifests/TLS patch above; **A** = [gateway auth](../../core/controlplane/gateway/auth/basic.go);
**U** = [user store](../../core/controlplane/gateway/auth/userstore_redis.go);
**N** = [NATS client](../../core/infra/bus/nats.go);
**P** = [policy signer](../../core/controlplane/gateway/handlers_policy_bundles_signing.go);
**H** = [audit chain](../../core/audit/chain.go).
"Planned" rows have no implementation evidence at the reviewed SHA.

| Credential / material | Holder | Purpose / authority | Storage / trust domain | Agent reachability | Status / source |
| --- | --- | --- | --- | --- | --- |
| Proposal-only Cordum API key | Agent runtime; gateway verifier | Authenticated proposal/status identity only; **no Kubernetes or protected-namespace authority** | Agent secret store; gateway auth state | Its own key only; never admin/bootstrap key | API-key mechanism exists (V/A); future proposal-only authorization planned (M2A/M2B) |
| Human account passwords and login session tokens | Named approver/admin; gateway | Approvals and role-scoped administration, not an agent identity | Human credential store/browser; gateway session state and user password hashes in trusted Redis | None | Existing local-user/session mechanism (V/D/A/U); profile provision not measured |
| Cordum bootstrap/service API key and bootstrap admin password | Trusted operator and gateway only | Establish/administer Cordum configuration and initial human account | Operator secret custody; management Secrets and gateway process | None; no embedded dashboard API key | Existing secret/config mechanism (V/D/S); automatic chart config-bootstrap job excluded from this profile |
| Management/target cluster operator credentials; infrastructure host-admin credentials | Named bootstrap/cluster/Redis/NATS operators | Install/maintain infrastructure, RBAC and secrets; privileged trusted authority separate from runtime approval | Operator-only vault/endpoints; never repository or agent-controlled CI | None, including indirect job execution | Required infrastructure trust assumption; provisioning/inventory planned (M2B), not supplied by this repo |
| Cluster-internal API/etcd/kubelet client/server keys and service-account signing private keys | Kubernetes infrastructure operators/components | Cluster control, node identity, storage and service-account issuance | Trusted cluster control-plane/node stores, inaccessible to application/agent workloads | None | Required infrastructure inventory at qualification (M2B); not created by chart |
| Management/application workload service-account tokens | No ordinary workload holder in this selected profile | No Kubernetes API authority for ordinary services or the reference application | Require automount and alternate delivery absent; future broker identity handled separately | None | Planned profile requirement; K disables automount, Helm equivalence not established |
| Future broker target credential | Future isolated broker only; trusted provisioner | Minimum mutation authority for named target namespace/objects, never cluster admin | Broker-only protected delivery/storage; no agent or application mount | None, directly or indirectly | Planned M2B-01; delivery/identity/RBAC undecided |
| Future broker/authority-service caller credential | Trusted broker/authority endpoints | Authenticate their control channel, not caller-selected target access | Trusted management custody; mechanism/delivery undecided | None | Planned M2A/M2B decision; no new credential format specified |
| Future grant issuer private material (if the ADR requires it) | Future authorized issuer only | Issue bounded execution authority; distinct from policy or audit keys | Issuer-only protected custody, no executor/agent distribution of private material | None | Planned M2A-01; public trust material listed separately below |
| Redis password | Authorized Cordum services; Redis verifier; trusted operator | Access control-plane state; not a target API credential | Management secret delivery/processes and Redis configuration | None | Existing V/D/S/T mechanism; selected external TLS Redis provision not measured |
| NATS client certificate private keys | Authorized Cordum services and trusted provisioner | mTLS bus identity; server-side authorization must limit use | Management TLS secrets/processes; NATS verifier has public trust only | None | Existing N/D/T mechanisms; external NATS mTLS profile planned; token/password/NKey alternatives absent |
| Redis client TLS private keys | Authorized Redis clients and provisioner | TLS client identity for state access | Management TLS secrets/processes | None | Existing D/T mechanism; no credential shared with agent |
| Gateway/Safety Kernel/context and external Redis/NATS server TLS private keys | Respective server operators/processes | Authenticate service endpoints | Server-only management/infrastructure secret stores | None | Existing D/T client/server wiring; external server provisioning planned |
| CA issuing private keys | Trusted PKI operators only | Issue service/cluster certificates; high-trust infrastructure authority | PKI custody outside agent and application domains | None | Required infrastructure allocation, not chart-managed; public CA roots below |
| Policy-signing private key | Trusted policy signer/gateway and provisioner | Sign approved policy content, not target execution grants | Management secret custody, not agent authors' repositories | None | Existing P mechanism; isolated profile provisioning planned |
| Audit HMAC key | Trusted audit writer/verifier and provisioner | Authenticate audit records; not an asymmetric public trust key or evidence of signer honesty | Management secret custody; any verifier holding it is trusted | None | Existing H mechanism; not a grant issuer or proof of target effect |
| Future evidence-reader credential | Independent SRE/security observer | Read target state/audit for qualification and reconciliation; no mutation | Trusted evidence domain | None | Planned M2B qualification fixture; exact access method undecided |
| License tokens; private registry/image-pull credentials | None | Community profile; public immutable images only | Absent | None | Excluded optional facilities (V: licensing, imagePullSecrets) |
| Backup/exporter credentials (object-store, webhook, Datadog, cloud IAM); external telemetry credentials | None | No backup/exporter/cloud integration in this bounded qualification profile | Absent; production operational qualification is separate | None | Excluded (V audit export/telemetry); Kustomize production backup is not imported |
| Worker/session/scheduler proof-signing keys; attestation/delegation credentials; Edge/shadow-detector credentials | None | No external workers, proof-signing/delegation/Edge/shadow integration in this profile | Absent | None | Excluded; V workerTrust off; neither grants nor broker authority may be inferred from these mechanisms |
| JWT HMAC/private keys, OIDC client credentials, cloud workload identities, application secrets | None | Local human sessions/API keys selected; benign reference application has no secrets or cloud identity | Absent | None | Excluded optional facilities (V/A); adding any requires inventory/boundary revision |

Trust roots are **not credentials**: public CA certificates and pinned target
identity; policy verification public keys; future grant-issuer public keys/trust
anchors and versions (M2A); and approved immutable artifact/image digests. Their
distribution must be integrity-controlled by trusted operators. Public values
need not be secret, but agents may not replace them. An HMAC verification key
is secret, unlike these public roots. JWT/OIDC, license and worker-proof trust
roots are absent with their excluded facilities.

The agent, repository scripts, plugins and agent-controlled CI must hold **no
protected-namespace production credentials**, nor recover them via mounts,
identity changes or privileged workloads. M2 qualification must enumerate
actual holders, permissions and delivery paths, including infrastructure and
operators; missing inventory or an unlisted credential invalidates the profile.
The table does not certify any present cluster or solve credential delivery.

## Boundary

The proposed boundary trusts declared Cordum services and authority storage,
the future broker, Kubernetes API/authentication/authorization/admission and
controllers, network isolation, secret provisioning, and their infrastructure
operators. It does not trust the agent, its repository contents, scripts,
plugins, dependencies, or agent-controlled CI. Management and bootstrap
credentials must stay outside that entire untrusted domain, including workloads
the agent proposes. Protected application workloads must not gain management
domain access or protected-namespace mutation authority.

There is no promise of protection against a hostile administrator of a trusted
endpoint, universal understanding of arbitrary program behavior, automatic
reversal of committed external changes, generic exactly-once external effects,
or honesty proved merely by a signed receipt. Resources outside the named
boundary are unsupported by this contract. A namespace label alone is not
isolation, and chart settings alone are not qualification evidence.

## Success criteria

The quoted bullets are verbatim, in order, from the human roadmap's section 1,
preserved in task comment `comment-98ec4fe1f5214e57a6ef90a5a72b81bf`
(2026-11-11). The tests below are **proposed, not implemented or run**.

Every future result must pin source SHAs, artifact digests, effective policy and
authority versions, executor/platform versions, deployment configuration, test
corpus, observation window, results and open exceptions. Independently captured
target audit/state must corroborate claimed effects and absence of effects;
missing evidence is inconclusive and never "verified". No current qualification
is implied by this document's validation.

| ID | Authoritative success bullet | Named test / setup | Measurable oracle | Required evidence | Status |
| --- | --- | --- | --- | --- | --- |
| SC01 | "The agent cannot perform that protected operation through an alternate path." | **Alternate-path exclusion**: exercise AP01-AP05 with synthetic identities in an isolated reachable target fixture; run an approved benign apply as a positive control. | Zero unauthorized writes for every AP row, including delayed/transient effects; the authorized control completes and its expected target change is observed. | Credential/identity inventory, attempt correlations, independent target audit and before/after state for intended/substituted targets, successful control receipt. | not measured |
| SC02 | "The approved artifact is the artifact used." | **Artifact identity**: approve immutable manifest input; record executor input independently; substitute bytes or resolved inputs before effect. | Approved input digest equals executor-used input for success; every substitution yields zero target writes. Stored YAML byte equality is not required. | Approved/executor input digests and provenance, authorization/attempt identifiers, target audit/state and rejection records; no secret input bytes in operator views. | not measured |
| SC03 | "Restrictions are enforced, not merely returned as JSON." | **Restriction efficacy**: enumerate every supported restriction and its effect-side enforcement point; submit compliant, violating, unsupported and mismatched cases. | Every supported restriction has an observed enforcing boundary; every violation/unsupported/mismatch case yields zero target writes, while a compliant control works. JSON-only restrictions cannot qualify. | Restriction-to-enforcement matrix, configuration/policy pins, positive/negative results, independent target audit/state. | not measured |
| SC04 | "Revocation, retries, crashes, and policy changes preserve authorization." | **Lifecycle authority**: revoke before effect, deliver concurrent duplicates/retries, interrupt after possible target commit before outcome persistence, and change policy while an operation waits. | Zero writes after revocation becomes effective within the published M2 bound; stale/policy-invalid authority yields zero writes. Retries cannot independently reuse consumed authority; uncertain outcomes remain unknown until reconciliation, with no blind replay or false success. | Authority/policy epochs and revocation timing, operation/attempt transitions, target audit/state and reconciliation decisions. Evaluate each case, including observed effects before a crash; no generic exactly-once or rollback claim. | not measured |
| SC05 | "Operators can reconstruct what happened without exposing secrets." | **Secret-safe reconstruction**: run accepted/rejected/unknown-outcome attempts with seeded synthetic secret canaries, then reconstruct each from operator-visible evidence. | Every attempted operation correlates decision, approval (or explicit not-required/denied state), operation, attempt and outcome; zero seeded canaries in operator-visible records/exports. Missing evidence prevents a verified result. | Correlation completeness report, independently retained target evidence, secret-canary scan counts and access/redaction configuration; protect the private canary fixture itself. | not measured |
| SC06 | "Safe work remains convenient enough that customers keep enforcement enabled." | **Safe-path usability**: perform 10 approved benign reference workflows with all required controls enabled. | 10/10 complete with expected target results and no disabled/bypassed controls; record added interactions and elapsed execution time excluding approval wait, with approval wait recorded separately. | Per-run controls/configuration, operator interaction counts, timings, approvals and confirmed target results. This is a future usability idea, not customer adoption or latency proof. | not measured |

Future lifecycle handling must distinguish proposed, authorized, claimed,
executing, confirmed success, confirmed failure and outcome unknown; unknown
outcomes require reconciliation before considering retry. A lost response after
a target commit is not proof of failure or permission to replay. Missing
authority/evidence prevents a verified success claim; an already committed
effect is not automatically reversed. Exact state encoding and recovery rules
remain M2A/M2B work. Document validation measures none of broker execution,
bypass resistance, full regression, latency or deployment qualification.

## Open questions

M2A-01 (`task-8018d60680844da9bdc9805b90f2e319`) owns the future grant/authority
ADR; M2B-01 (`task-0c4e8c43b2a44d9593e0e20b84888c41`) owns the future broker
ADR. Neither design nor implementation is decided by this contract.

**M2A-01 — grant and durable authority ADR:**

- Which existing encoding/primitives express authority without a new crypto
  scheme, and how are tenant, requester/approver, immutable inputs, target,
  restrictions and operation identity bound?
- How are policy/authority epochs, expiry and revocation checked immediately
  before effects; what is the published revocation bound and fail-closed behavior
  under unavailable state, concurrent claims and intervening policy changes?
- How are grant/state versions, issuer trust, key custody/rotation and public
  trust distribution handled without expanding authority on migration?

**M2B-01 — broker and reference-deployment ADR:**

- Where does the broker physically run, how is the caller authenticated, and
  how are target credentials delivered/rotated without any agent-reachable path?
- Which target RBAC, admission, network and workload restrictions enforce the
  closed resource set and exclude privilege/identity/secret escalation?
- Which apply/dry-run/field-ownership/conflict/precondition semantics bind the
  exact approved input and object state; how are partial multi-object effects
  represented without promising atomicity or automatic rollback?
- What receipts and independent target observations distinguish confirmed
  success/failure from unknown outcome, and how does reconciliation authorize
  any later attempt without blind replay?
- Which isolated test environment, identities, target observers and positive
  controls will qualify AP01-AP05 and SC01-SC06 on pinned artifacts/configuration?
