# Safety policy `constraints` extensions

`rule.constraints` on the safety policy schema accepts three additional
fields used by the CONSTRAIN-extension primitives Cord-Claw ships:

- `max_output_bytes` — caps the size of outputs an action can produce.
- `allowed_destinations` — allowlist of destinations (file paths, URLs,
  topic targets) the action may write to.
- `redact_patterns` — regex patterns whose matches in output get
  redacted before downstream emission.

These fields live alongside the existing `budgets`, `sandbox`,
`toolchain`, `diff`, and `redaction_level` constraint primitives.

## Schema bounds

| Field | Type | Bounds |
| --- | --- | --- |
| `max_output_bytes` | integer | `[0, 16777216]` (0 to 16 MiB) |
| `allowed_destinations` | array of string | unbounded count; each entry is an opaque string the consumer interprets |
| `redact_patterns` | array of string | unbounded count; each entry is a regex pattern the consumer interprets |

The 16 MiB upper bound on `max_output_bytes` is hard-enforced by the
schema to prevent OOM via misconfigured large outputs. Operators who
need higher caps should configure them via per-deployment policy
overrides, not via per-rule constraints.

`additionalProperties: false` stays on the `constraints` object, so
typos like `max_output_byte` (missing `s`) still fail validation. The
schema is the single source of truth for the constraint vocabulary.

## Emission semantics

These constraints fire when the safety kernel emits a Decision with
`Type = allow_with_constraints`. The unified Decision shape (see
`core/policy/types.go`) carries the constraints payload to downstream
enforcement, which:

1. Truncates outputs exceeding `max_output_bytes` and tags the
   truncation in the Decision trace.
2. Rejects writes to destinations not present in `allowed_destinations`
   (when the list is non-empty).
3. Applies `redact_patterns` to the output stream before persistence
   and downstream emission.

## Backward compatibility

Existing policy fragments without any of these three fields validate
unchanged. The extension is additive only — there is no schema-version
bump and no migration step. Operators can adopt the new fields
incrementally as their packs need them.

## Example: result-gating rule

```yaml
version: "1"
default_tenant: default
rules:
  - id: openclaw-result-gating
    decision: allow_with_constraints
    reason: enforce output budget + redact secrets
    match:
      topics: ["job.openclaw.result_gating"]
    constraints:
      max_output_bytes: 65536
      allowed_destinations:
        - "file://workspace/*"
        - "s3://artifacts/*"
      redact_patterns:
        - "secret_[a-z0-9]+"
        - "(?i)password=\\S+"
```

## Why these are explicit properties

Setting them as explicit schema properties (rather than relaxing
`additionalProperties` to `true`) preserves the strict-by-default
posture of the constraints object. Future constraint primitives go
through the same explicit-property addition path; operators can rely on
typo detection to surface mistakes at install time rather than at
emit time.
