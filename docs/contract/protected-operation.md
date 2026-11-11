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

The selected credential profile, trust-domain flows and future qualification
requirements are specified below in the next contract increment. No broker is
installed or configured by this document.

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

Six measurable acceptance ideas will map, in order, to the human roadmap's
section 1 bullets preserved in task comment
`comment-98ec4fe1f5214e57a6ef90a5a72b81bf` (2026-11-11).
All are future checks with status **not measured**; document validation is not
broker execution, bypass resistance, full regression, latency or deployment
qualification.

## Open questions

M2A-01 (`task-8018d60680844da9bdc9805b90f2e319`) owns the future grant/authority
ADR; M2B-01 (`task-0c4e8c43b2a44d9593e0e20b84888c41`) owns the future broker
ADR. Neither design nor implementation is decided by this contract.
