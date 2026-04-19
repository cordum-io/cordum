# MCP Prompts

Cordum ships a small set of **server-rendered MCP prompts** — templated
inputs an LLM client (Claude Code, Cursor, VS Code) can request by
name. The server fills in Cordum-specific context (policy grammar,
audit lookups, version diffs) and returns a chat-shaped message chain
the client feeds to its model.

Every prompt is admin-gated at the gateway layer (same RBAC as the
corresponding REST endpoints). Prompts never expose raw tenant secrets
or signed bundle bytes — they render decision metadata + grammar
descriptions the client's LLM turns into prose.

All four first-party prompts are registered at gateway boot via
`mcp.RegisterAllPrompts(registry)`. An MCP client lists them via
`prompts/list` and renders a specific one via
`prompts/get` — the standard MCP protocol methods.

## draft_safety_rule

Scaffold a new Cordum safety-policy rule for a described scenario.

| Argument     | Type   | Required | Description                                    |
| ------------ | ------ | -------- | ---------------------------------------------- |
| `scenario`   | string | yes      | Plain-language description of the behaviour.   |
| `topic`      | string | no       | Target job topic glob (e.g. `job.payments.*`). |
| `risk_level` | enum   | no       | `low` \| `medium` \| `high` (default `medium`).|

**Rendered output shape** (two messages):

1. System message: Cordum policy-bundle YAML grammar summary + 5
   decision sentinels + the verbatim simulate-before-apply disclaimer.
2. User message: echoes scenario/topic/risk_level + instructs the
   model to emit a ```yaml-fenced rule, the disclaimer, and a
   two-sentence rationale.

**Safety disclaimer** (embedded verbatim in every render):

> Always run the scaffolded rule through `/api/v1/policy/simulate` on
> a staging tenant before promoting it to production. Policy changes
> are signed; a mistake at this layer can block real jobs.

**Recommended model class**: reasoning model (Claude Opus / Sonnet,
GPT-4.1) — drafting safe YAML needs careful constraint satisfaction.

**Safety note**: The rendered prompt is always a scaffold — the
operator MUST simulate against staging before promoting. Never
pipe the LLM's output straight into `cordumctl policy publish`.

## explain_denial

Translate a Cordum deny decision into plain English with actionable
remediation advice.

| Argument | Type   | Required | Description                      |
| -------- | ------ | -------- | -------------------------------- |
| `job_id` | string | yes      | The denied job's identifier.     |

**Server-side fetch**: the prompt consults a
`DenialContextFetcher` wired onto the request context by the gateway.
When present, the fetcher reads the deny event from the tenant audit
chain and embeds the real decision + rule_id + reason + job context
into the user message. When no fetcher is wired (dev deploys, stdio),
the prompt asks the operator to paste the event into the conversation
manually — never hallucinates missing fields.

**Rendered output shape** (two messages):

1. System message: "You are a Cordum policy explainer" + 4 numbered
   requirements (name the rule, restate impact, suggest one of three
   remediations, never invent fields).
2. User message: the decision context (rule_id, reason, tenant,
   topic, agent_id, risk_tags, occurred_at) — or a pasteable template
   when no fetcher is wired / the fetch failed.

**Recommended model class**: small model (Claude Haiku, GPT-4.1-mini)
suffices — the LLM summarises a structured record, no reasoning load.

## summarize_approvals

Natural-language digest of approvals activity over a time window.

| Argument  | Type     | Required | Description                                      |
| --------- | -------- | -------- | ------------------------------------------------ |
| `window`  | duration | no       | Lookback: `Nh` or `Nd` (default `24h`, max `30d`). |
| `tenant`  | string   | no       | Tenant slug (defaults to the session tenant).    |

**Window format**: the server accepts `1h`…`720h` (30 days) and
`1d`…`30d`. Other forms — including `ms`, `s`, `m`, `w`, or negative
values — are rejected with `-32602 invalid params`.

**Server-side fetch**: an `ApprovalsSummarySource` wired on the
request context returns `{Pending, Approved, Rejected, Expired, ByRule
map, Approvers map}`. The server renders a compact digest (counts +
top-5 rules + top-5 approvers). No source wired → asks the operator
to paste.

**Rendered output shape**: system prompt asks for a 5-part summary
(headline, top rules, top approvers, anomalies, explicit "data
unavailable" fallback); user message carries the digest or a
paste-prompt.

**Recommended model class**: reasoning model — spotting anomalies
benefits from pattern recognition.

## policy_migration_helper

Convert a Cordum policy bundle from one grammar version to another
using an operator-supplied diff of grammar changes.

| Argument       | Type   | Required | Description                         |
| -------------- | ------ | -------- | ----------------------------------- |
| `from_version` | string | yes      | Source bundle version (e.g. `2024-09-01`). |
| `to_version`   | string | yes      | Target bundle version (e.g. `2025-03-01`). |

Same-version requests (`from_version == to_version`) are rejected —
a no-op migration is a usage error, not a silent success.

**Server-side fetch**: a `PolicyGrammarDiffSource` wired on the
request context returns a plain-text grammar diff between the two
versions. The server embeds the diff verbatim under `grammar_diff:`.
Absent source → asks the operator to paste the changelog.

**Rendered output shape**: system prompt pins 5 requirements (apply
every diff entry, preserve id/decision/reason unless renamed, flag
lossy conversions with `# MIGRATION-REVIEW`, emit ```yaml-fenced
output, re-sign + simulate reminder); user message holds the diff +
"paste the bundle to convert" prompt.

**Recommended model class**: reasoning model — grammar translation
across versions requires careful constraint satisfaction.

**Safety note**: The output is a draft — the operator MUST re-sign
the converted bundle (`cordumctl policy sign`) and simulate it
against historical jobs before promoting. The system prompt
reminds the LLM to surface this reminder in its final line; tests
pin that phrase.

## Data-source injection

Three of the four shipped prompts (`explain_denial`,
`summarize_approvals`, `policy_migration_helper`) read from
request-context-carried data sources instead of importing backend
packages directly. This keeps `core/mcp` free of `core/audit` +
`core/policy` import edges and lets any caller — gateway, tests,
stdio — wire its own implementation.

The contract is three `WithXxx(ctx, fetcher)` helpers in
`core/mcp/prompts.go`:

- `WithDenialContextFetcher(ctx, fn)`
- `WithApprovalsSummarySource(ctx, fn)`
- `WithPolicyGrammarDiffSource(ctx, fn)`

Gateway boot calls these before dispatching the `prompts/get` request
so the renderer finds its source on the context. Tests exercise the
context-flow path via the `requestCtx` field on `JSONRPCMessage`
(see `TestPromptsGet_ContextFlowsToFetcher`).

## Cross-references

- [MCP tools](./tools.md) — the tool surface prompts operate alongside.
- [MCP resources](./resources.md) — cordum:// URI scheme.
- [Mutating tools](./mutating-tools.md) — mutating tool surface + approval flow.
- [Scope preapproval](./scope-preapproval.md) — CI-bot preapproval (admin-gated).
- Per-tool approval gates, outbound signing, and scope filtering — see the corresponding docs on [GitHub](https://github.com/cordum-io/cordum/tree/main/docs/mcp) (`per-tool-approval.md`, `outbound-signing.md`, `scope-filtering.md`).
