# REQUIRE_HUMAN threshold — input-rule downgrade routing

> `core/infra/config/safety_policy.go` — `RequireHumanThreshold`
> `core/controlplane/safetykernel/input_policy.go` — `shouldDowngradeDenyToRequireHuman`
> `core/controlplane/safetykernel/kernel.go` — input-rule dispatch wire

Cordum's safety kernel emits the existing `pb.DecisionType` values per
request: `DENY`, `REQUIRE_HUMAN`, `ALLOW`, `ALLOW_WITH_CONSTRAINTS`. The
threshold defined here turns a subset of `DENY` outcomes — those where
the matched input rule's finding is *truly ambiguous* — into
`REQUIRE_HUMAN` instead, so an operator-side reviewer can recover the
job rather than the request failing closed.

This is a **2-output dial**, not a 3-output routing fork: rules whose
finding meets the configured severity/confidence floor stay `DENY`;
rules below the floor (or rules whose request carries no
`ActionDescriptor`) downgrade to `REQUIRE_HUMAN`. No new
`DecisionType` value is introduced; the existing `REQUIRE_HUMAN`
audit/dashboard/approval-store path carries the downgraded decisions
through to the operator.

## Why 2-output, not 3

The earlier draft of this feature (rejected commit `cf40ce81`,
1138 LOC deleted in `75ed120d`) used a 3-output model: `DENY` for
high-severity action-bound, `REQUIRE_HUMAN` for medium-ambiguous, and
`ALLOW` for prompt-only "educational" content tagged via a session
metadata `educational_context` field. That session-metadata carrier
does not exist in the current architecture. Per architect amendment
`comment-79a9e609` on task-96f931fe, building one is a separate piece
of work that touches gateway session creation, auth context, and the
entire decision-input contract.

The 2-output dial sidesteps the carrier dependency: ambiguous content
that today returns a hard `DENY` instead returns `REQUIRE_HUMAN`, which
is already a valid outcome under DoD #4 ("allowed or require-human
only when truly ambiguous"). The full educational-context allow path
remains future work.

## Configuration

The threshold lives on `config.SafetyPolicy.RequireHuman`:

```yaml
require_human:
  min_severity_for_deny: high      # findings below this floor downgrade
  min_confidence_for_deny: 0.8     # findings below this confidence downgrade
  downgrade_when_prompt_only: true # any DENY without an ActionDescriptor downgrades
```

| Field | Type | Effect on a matched `deny` rule |
| --- | --- | --- |
| `MinSeverityForDeny` | string (`low`/`medium`/`high`/`critical`) | Finding severity strictly below this floor downgrades to `REQUIRE_HUMAN`. Operator-authored rule-tier severity is also consulted — a `low`-tier rule downgrades even when its synthesized findings carry a higher severity. Empty string = no severity floor (legacy DENY-everything). |
| `MinConfidenceForDeny` | float32 (0.0–1.0) | Finding confidence strictly below this floor downgrades. Zero = no confidence floor. |
| `DowngradeWhenPromptOnly` | bool | If true, any matched `deny` rule on a request without an `ActionDescriptor` (`input.Action == nil`) downgrades regardless of severity/confidence. |

The three conditions are **logical OR** — meeting any one triggers
downgrade. The "at least one below" semantics on findings (not "all
below") matches operator intent: a multi-pattern rule that authored a
clean DENY expectation downgrades the whole rule the moment one
matched pattern carries ambiguous severity/confidence.

**Zero-value safety**: an unset `RequireHuman` (every field at its
zero value) preserves the legacy behavior. Existing deployments that
have not opted into REQUIRE_HUMAN routing see no change.

## Routing table

| Rule decision | Finding severity | Finding confidence | Action descriptor | Resulting `DecisionType` |
| --- | --- | --- | --- | --- |
| `deny` | ≥ floor | ≥ floor | non-nil | **DENY** (unchanged from today) |
| `deny` | < floor | any | any | REQUIRE_HUMAN |
| `deny` | any | < floor | any | REQUIRE_HUMAN |
| `deny` | any | any | nil (prompt-only) ✱ | REQUIRE_HUMAN |
| `require_approval` / `require-approval` / `require_human` | any | any | any | REQUIRE_HUMAN (unchanged) |

✱ Only when `DowngradeWhenPromptOnly: true`. Default `false` preserves
legacy prompt-only DENYs.

The downgrade short-circuits the rest of the kernel pipeline:
`approval_required = true` on the response, the SIEM audit event for
the input rule records the rule's `id` and the original DENY reason
text, and the existing `core/edge/approvals` store provisions the
human-review record.

## The 5 false-positive scenarios

DoD #4 lists five concrete defensive/educational content patterns that
were returning `DENY` even though they're not action-bound abuse.
After this threshold lands (with the recommended posture
`MinSeverityForDeny: high, MinConfidenceForDeny: 0.8,
DowngradeWhenPromptOnly: true`), each routes to `REQUIRE_HUMAN`
instead:

1. **Defensive `/etc/passwd` mention** — security runbook explains
   credential paths. `act == nil`, finding severity = medium,
   confidence ≈ 0.6 → `REQUIRE_HUMAN`.
2. **`rm -rf` mention in defensive runbook** — non-executed reference.
   `act == nil`, severity = medium, confidence ≈ 0.55 → `REQUIRE_HUMAN`.
3. **API-key rotation procedure** — describes how to rotate without
   embedding a key value. `act == nil`, severity = medium, confidence
   ≈ 0.7 → `REQUIRE_HUMAN`.
4. **Approval-token logging in compliance docs** — no token value.
   `act == nil`, severity = medium, confidence ≈ 0.65 → `REQUIRE_HUMAN`.
5. **Metadata-service `169.254.169.254` education** — no outbound URL
   action. `act == nil`, severity = medium, confidence ≈ 0.7 →
   `REQUIRE_HUMAN`.

Test fixtures live in
`core/controlplane/safetykernel/decision_threshold_test.go`
(`TestShouldDowngradeDenyToRequireHuman_FalsePositiveScenarios`),
table-driven across the five scenarios with mutation-resistant
`assertEquals` on the boolean outcome.

## Anti-patterns and guarantees

- **No session-metadata carrier is consulted.** The threshold reads
  only the matched rule's tier severity, the finding's severity +
  confidence (already produced by the existing scanners), and whether
  the request carried an `ActionDescriptor`. There is no
  `EducationalContext` or `defensive_context` claim flowing through.
- **`input_text` is never trusted to declare context.** An attacker
  cannot spoof "this is educational" via input content because no such
  field is read.
- **Action-bound DENYs are unchanged.** A request with a non-nil
  `ActionDescriptor` whose finding is high-severity and high-confidence
  stays `DENY` — the architect amendment explicitly carved out the
  "unchanged from today" branch, and DoD #6 forbids masking
  action-layer misses.
- **Output policy (post-execution content scanning) is out of scope.**
  This threshold applies only to the input-rule dispatch in
  `kernel.go` — `output_policy.go`'s `ALLOW`/`QUARANTINE`/`REDACT`
  routing is a separate decision-space and is not affected.

## Observability

- The structured log `input rule matched` (kernel.go input-rule dispatch
  loop) now also carries the resolved `outputDecision` field so
  operators can see at a glance when a `deny`-authored rule routed to
  `require_human` instead.
- The existing approvals audit + SIEM event paths for `REQUIRE_HUMAN`
  pick up the downgraded decisions transparently — no new event types
  or audit-schema changes.
- The dashboard governance timeline at `/api/v1/governance/decisions`
  already renders `REQUIRE_HUMAN` per the existing surface; no
  dashboard work is needed unless an operator wants a "downgraded
  from DENY" subtype, which would be a follow-up task.

## Implementation references

- `core/infra/config/safety_policy.go` — `RequireHumanThreshold` struct
  + `SafetyPolicy.RequireHuman` field.
- `core/controlplane/safetykernel/input_policy.go` —
  `shouldDowngradeDenyToRequireHuman` helper +
  `severityRank` ordinal mapping.
- `core/controlplane/safetykernel/kernel.go` — `server.requireHumanThreshold`
  field, policy-load wire at `setPolicyWithInvariants`, RLock-snapshot
  read in `Check`, dispatch downgrade inside the input-rule loop.
- `core/controlplane/safetykernel/decision_threshold_test.go` — 9
  table-driven tests covering 5 FP scenarios + action-bound stays-DENY
  + zero-threshold legacy preservation + rule-severity-floor
  precedence + severityRank ordinal mapping.

## Authority trail

- Task: `task-96f931fe` "Tune existing decision thresholds for REQUIRE_HUMAN"
- Governor amendment scope: `comment-e58c8328` (2026-05-16T09:37Z) —
  forbade `core/policy/actiongates/*`; required code in
  `core/controlplane/safetykernel/{scanners,output_policy,kernel_actiongate}.go`
  + `core/infra/config/safety_policy.go`.
- Architect amendment routing: `comment-79a9e609` (2026-05-16T09:38Z) —
  2-output model, drop EducationalContext, `RequireHumanThreshold`
  struct, 5 FP scenarios assert `DENY → REQUIRE_HUMAN`.
- Worker framing of reopen #1: `comment-e3aa1a2f` (worker-f7cb85a0,
  2026-05-16).
- Governor playbook authorizing path (1) re-implement-now:
  `msg-c42abf9c`.
- Rejected predecessor commit: `cf40ce81` (1350 LOC in forbidden dir);
  deleted in `75ed120d`.
