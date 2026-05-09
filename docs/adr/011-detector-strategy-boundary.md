# ADR-011: Detector strategy boundary for content classification

- Status: Accepted
- Date: 2026-05-09
- Task: `task-3a25ba1f`
- Related epic: `epic-f3da4017` AgentShield Benchmark Hardening & Action-Layer Gates

## Context

The AgentShield public corpus is useful as a smoke test for the exact
adapter/config path under test, but it is not a generalization metric. A
model or detector can score well by matching known-public strings. Cordum's
private holdout and mutation regression work exists because public-corpus
performance alone can hide brittle detector behavior.

Cordum's product differentiator is not generic jailbreak or content
moderation. Cordum is the control plane that governs agent **actions** before
or around execution: file/resource access, URL/exfiltration paths,
destructive mutations, MCP tool calls, tenant boundaries, approvals,
delegations, and audit-chain provenance. Those controls are strongest when
they use backend-verifiable request metadata and signed/audited evidence, not
free-form user claims inside a prompt.

Existing Cordum detector surfaces are narrow and deterministic:

- input policy is evaluated synchronously before scheduler dispatch;
- output safety combines metadata checks with optional content scans;
- output scanners in `core/controlplane/safetykernel/scanners.go` and
  `config/output_scanners.yaml` are regex/keyword detectors for secrets, PII,
  code-injection fragments, and prompt-injection fragments.

The question for this ADR is how far Cordum should go on semantic content
classification before broad detector investments drift the product toward a
"Lakera-lite" category.

## Forces

| Force | Why it matters |
| --- | --- |
| Determinism | Operators must understand why an action was allowed, denied, quarantined, or sent for approval. |
| Pre-dispatch latency | ADR-001 sets a synchronous Safety Kernel goal of `<5 ms p99`; model calls cannot live inside that hot path. |
| Explainability | Audit evidence must identify rule IDs, matched metadata, scanner names, offsets, and provenance. |
| Offline/on-prem operation | Cordum must remain deployable without a SaaS classifier dependency. |
| Testability | Gates must have stable unit/regression tests and must not be tuned to private holdout prompts. |
| Maintenance burden | Broad regex catalogs and prompt classifiers need continuous attack-content curation. |
| Privacy | Prompt/output content may include secrets, PII, customer source code, or regulated data. |
| Differentiation | Cordum wins by proving action governance and provenance, not by competing with content-moderation providers. |

## Options considered

| Option | Determinism / latency | Explainability / testability | Offline / privacy | Maintenance burden | Differentiation and fail mode |
| --- | --- | --- | --- | --- | --- |
| 1. Deterministic typed scanners + action-layer gates | Strong. Metadata/action checks and narrow scanners stay deterministic and can fit pre-dispatch budgets when they avoid large content scans. | Strong. Tests assert exact rule/scanner IDs, fields, offsets, and decisions. | Strong. Runs locally with no content egress. | Moderate. Requires careful typed rules but not broad semantic curation. | Best fit. Fail closed for policy/checker errors, with explicit low-risk fail-open modes where already documented. |
| 2. Richer regex/heuristics catalog | Deterministic but brittle. Latency usually acceptable, but large catalogs can grow cost and false positives. | Medium. Matched patterns are explainable; semantic intent is not. Tests often overfit examples. | Strong if local, but false positives can leak user trust. | High. Requires constant corpus updates and normalization tricks. | Useful only for narrow finding classes; poor as a generic jailbreak classifier. |
| 3. Local model-in-loop classifier | Probabilistic and slower than the `<5 ms p99` Safety Kernel target. | Medium/low. Scores are auditable but not inherently explainable; tests can be flaky across model versions. | Medium. On-prem is possible but operationally heavy; model artifacts and GPU/CPU capacity become product requirements. | High. Needs model lifecycle, evaluation, prompt privacy, and drift management. | Allowed only as an optional integration outside the core pre-dispatch hot path. Fail closed for high-risk tenants, deterministic fallback required. |
| 4. External classifier integration | Probabilistic and network-bound; cannot be required for core dispatch. | Medium. Provider labels can be logged, but behavior depends on provider policy/versioning. | Low/medium. Content leaves the deployment boundary unless customer explicitly configures a trusted provider. | Medium. Less model ops, more vendor/latency/outage management. | Appropriate as customer-provided upstream content classification, not as Cordum core. Fail mode must be tenant-configured and auditable. |
| 5. Upstream-only classifier boundary | Keeps Cordum deterministic and delegates broad semantic moderation to a dedicated layer before Cordum receives the action. | Strong for Cordum's scope: Cordum audits the upstream verdict as input evidence plus its own action-layer decision. | Strong when customer chooses their own provider/on-prem classifier. | Low for Cordum; classifier maintenance belongs to the upstream component. | Best product boundary for generic jailbreak/content moderation. Cordum still governs the resulting action and provenance. |

## Decision

Cordum's default detector strategy is:

1. **Implement deterministic action-layer gates and narrow typed scanners in
   Cordum core.** The native surface is backend-verifiable metadata and
   action evidence: topic, tenant, capability, risk tags, MCP server/tool,
   file/resource/URL target, approval/delegation record, signed audit state,
   output pointer metadata, and narrow scanner findings.
2. **Use regex/heuristics only for typed, explainable finding classes.**
   Examples that stay in category are secret leakage, PII artifacts,
   shell/SQL/code-injection fragments, and explicit prompt-injection phrases
   in output scans. Patterns must be narrow, unit-tested, and tied to a
   concrete decision (`deny`, `require_approval`, `redact`, or `quarantine`).
3. **Do not build a generic jailbreak/content-moderation classifier in Cordum
   core.** Broad semantic questions such as "is this prompt adversarial in a
   general content-policy sense?" are out-of-category.
4. **Treat model-in-loop classification as an optional integration boundary,
   not the core dispatch mechanism.** Customers may place an upstream
   classifier before Cordum or configure an optional classifier integration,
   but Cordum's Safety Kernel hot path remains deterministic.
5. **Audit upstream/model verdicts as evidence, never as user claims.** A
   prompt that says "approved by CFO" is not evidence. A signed approval
   record, delegation token, upstream classifier verdict with provider/version,
   or backend-owned policy decision can be evidence.

## Model-in-loop constraints

No model-in-loop classifier is approved inside the synchronous Safety Kernel
pre-dispatch evaluator. ADR-001's `<5 ms p99` policy-before-dispatch budget
belongs to deterministic policy evaluation.

If a future PRD approves a model-in-loop or external classifier integration,
it must satisfy all of these constraints before implementation:

- **Placement:** run upstream of Cordum job submission, at a gateway
  pre-admission layer, or in the existing output-safety async/deeper-content
  path. It must not block the core Safety Kernel hot path.
- **Latency budget:** gateway/pre-admission classifier p95 <= 300 ms and p99
  <= 750 ms per classifier call; async output-content checks use the existing
  output-safety content-check budget and keep results quarantined while
  fail-closed tenants wait.
- **Fail mode:** high-risk/regulatory tenants fail closed on classifier
  timeout/error; fail-open is allowed only for explicitly configured low-risk
  tenants or non-production environments, and must increment an audit/metric
  counter.
- **Privacy boundary:** content may be sent only to a tenant-approved
  provider or tenant-owned local model. No provider training/retention unless
  the tenant explicitly opts in. Redaction/minimization happens before egress
  where possible.
- **Offline/on-prem story:** deployments without the provider must continue to
  operate with deterministic Cordum gates; optional local classifiers must be
  versioned and pinned.
- **Deterministic fallback:** a model verdict may add evidence or request
  human review, but deterministic allow/deny/quarantine rules must define the
  fallback behavior when the classifier is absent or unhealthy.
- **Audit evidence:** record provider/model name, version, policy set,
  latency, confidence/labels, fail mode, and the deterministic Cordum rule that
  consumed the verdict.

## Out of category

These are not Cordum-core detector work unless a later ADR explicitly changes
this boundary:

- generic jailbreak scoring for arbitrary prompts;
- general content moderation (hate, self-harm, adult, political persuasion,
  brand safety, etc.);
- provider policy enforcement or model-safety alignment;
- broad semantic intent classification detached from a concrete Cordum action;
- user-claimed trust/provenance strings inside the prompt.

Cordum can ingest an upstream verdict for any of the above as evidence, then
apply deterministic action-layer policy to the requested action.

## Consequences

Positive:

- Later gate tasks can cite a clear boundary: deterministic action governance
  and narrow typed scanners are in scope; broad semantic classifiers are not.
- Private holdout/mutation tests remain a regression guard instead of a list of
  strings to tune against.
- The Safety Kernel keeps the policy-before-dispatch latency and offline
  properties that make Cordum deployable in regulated environments.
- Product messaging can truthfully say Cordum governs agent actions with
  backend-verified provenance rather than claiming universal content security
  accuracy.

Tradeoffs:

- Cordum will not catch every novel prompt-injection string with native regex
  scanners alone.
- Customers who need deep content moderation must integrate an upstream
  classifier or accept deterministic-only coverage.
- Narrow scanners still require curation and false-positive management; they
  must remain tied to typed findings and not sprawl into a generic corpus match
  list.

## Follow-up links

- `docs/agentshield-scope-boundary.md` — public AgentShield score framing and
  the benchmark/generalization boundary.
- `docs/adr/001-safety-before-dispatch.md` — deterministic policy-before-
  dispatch guarantee and latency target.
- `docs/safety-kernel.md` — input/output policy behavior and failure modes.
- `docs/output-safety.md` — output scanner and content-check boundaries.
- `task-184457f7` — multi-agent governance gates, which should use
  backend-verifiable action/provenance evidence.
- `task-7fbc245d` — private holdout/mutation regression suite, used to detect
  overfitting rather than to tune public strings.
