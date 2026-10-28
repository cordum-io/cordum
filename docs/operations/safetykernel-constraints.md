# Safety policy `constraints`

`rules[].constraints` takes the keys that `config.PolicyConstraints` parses
(`core/infra/config/safety_policy.go`). They are the same fields the Safety
Kernel returns on an `allow_with_constraints` decision via the CAP
`PolicyConstraints` message.

| Key | Fields |
| --- | --- |
| `budgets` | `max_runtime_ms`, `max_retries`, `max_artifact_bytes`, `max_concurrent_jobs` |
| `sandbox` | `isolated`, `network_allowlist`, `fs_read_only`, `fs_read_write` |
| `toolchain` | `allowed_tools`, `allowed_commands` |
| `diff` | `max_files`, `max_lines`, `deny_path_globs` |
| `redaction_level` | string |

The schema sets `additionalProperties: false` at every level, so unknown keys
and typos fail validation instead of being dropped. A drift test
(`TestSafetyPolicySchemaMatchesStruct`) keeps the schema and the struct in
sync. The only exception is the three deprecated keys below.

```yaml
version: "1"
rules:
  - id: pack-action-budget
    decision: allow_with_constraints
    reason: bound runtime for pack actions
    match:
      topics: ["job.pack.action"]
    constraints:
      budgets:
        max_runtime_ms: 30000
      redaction_level: moderate
```

## Deprecated keys: `max_output_bytes`, `allowed_destinations`, `redact_patterns`

The schema still accepts these three keys under `rules[].constraints`, so
policies that use them keep loading. They have no effect. `PolicyConstraints`
and the CAP `PolicyConstraints` message have no such fields, so
`ParseSafetyPolicy` drops them and the Safety Kernel never enforces them. The
schema keeps their old types, and `max_output_bytes` must still be an integer
from 0 to 16777216 (16 MiB). They are deprecated, so remove them from your
policies.

Use these instead:

- `budgets`, which the scheduler applies to the job. `max_runtime_ms` caps
  the job deadline. `max_artifact_bytes`, `max_retries` and
  `max_concurrent_jobs` reach the worker as `CORDUM_MAX_ARTIFACT_BYTES`,
  `CORDUM_MAX_RETRIES` and `CORDUM_MAX_CONCURRENT_JOBS`.
- Output rules (see [output-policy.md](../output-policy.md)).
  `output_rules[].match.max_output_bytes` matches outputs larger than the
  given size. Use `decision: quarantine` when matching sensitive output must
  be withheld. Synchronous `redact` handling replaces the result pointer with
  sanitized content, or quarantines when sanitized content is unavailable.
  The asynchronous content check records a `REDACT` decision but does not
  replace the job's result pointer or quarantine that decision, so matching
  content can remain accessible through the original pointer. Do not rely on
  asynchronous `redact` for sanitization. Asynchronous quarantine also runs
  after job completion; it does not guarantee that output was never visible
  before the check finished.
