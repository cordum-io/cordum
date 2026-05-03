# Internal docs

**Internal docs only — not for public consumption / customer support / external sharing.**

These docs contain operational details, threat-model code paths, internal
audit findings, and enterprise entitlement matrices intended for Cordum
engineering only. Public docs at `docs/` (one level up) MUST NOT link in;
internal docs MAY link to public docs but the reference graph is one-way.

Classification rules and the full triage table for every `*.md` file in
`docs/` live in `docs/visibility-policy.md` (public).

## Subtree

- `_audit/` — historical sync audits (e.g. quickstart drift checks).
- `bug-hunts/` — internal bug-hunt audits with code-path analysis.
- `cleanup/` — pre-GA legacy / dead-code sweep audit trail (epic-1cadd6f2).
- `decisions/` — internal decision log (rejected approaches, trade-off
  analyses with task references).
- `edge/` — Edge P0 acceptance evidence (operational, EDGE-032 closure).
- `heartbeat-demotion-audit.md` — call-site catalog for the Phase-2
  heartbeat-demotion rewire.
- `issue-drafts/security/` — internal SEC-* security issue drafts with
  severity ratings.
- `llmchat/` — vLLM configuration verification (LLM chat epic deferred,
  retained for archive).
- `security/` — Edge P0 threat model, enterprise entitlement matrix,
  RBAC route audit. Operational sensitive.
