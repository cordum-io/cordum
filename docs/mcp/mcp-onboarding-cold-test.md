# MCP Onboarding Cold Test — Human Tester Checklist

> **This document is filled in by a human tester, not by an AI worker.**
> Precedent: task-75273093. An AI worker claiming this checklist is
> complete is a DoD-falsifying regression — reopen the task.

This checklist mirrors the three public onboarding docs and exists so
we have independent human confirmation that a new evaluator, starting
from zero, can get from "I have the client installed" to "I'm issuing
a governed tool call against Cordum" in under 5 minutes.

The getting-started entrypoints (DoD-named paths, per-client, each
with screenshot slots the tester fills in):

- [docs/getting-started/mcp-with-claude-code.md](../getting-started/mcp-with-claude-code.md)
- [docs/getting-started/mcp-with-cursor.md](../getting-started/mcp-with-cursor.md)
- [docs/getting-started/mcp-with-vscode.md](../getting-started/mcp-with-vscode.md)

The canonical deep quickstarts (wired into docs-site/sidebars.ts under
"Drive Cordum with MCP") cover the full stdio + HTTP + TLS surface:

- [docs/mcp/quickstart-claude-code.md](./quickstart-claude-code.md)
- [docs/mcp/quickstart-cursor.md](./quickstart-cursor.md)
- [docs/mcp/quickstart-vscode.md](./quickstart-vscode.md)

Both sets share the same five steps; the getting-started variants
carry the screenshot slots and the canonical variants carry the deep
troubleshooting.

## Screenshot capture protocol

Every getting-started doc embeds four image references under
`docs/static/img/mcp-<client>/`. The human tester captures each PNG
at the documented step during this cold test, redacts real tenant
IDs + API keys, and commits the PNGs alongside the filled-in report
at `docs/mcp/cold-tests/<date>-<client>-<tester>.md`. Dimensions:
1280×800 default. See each directory's README.md for the per-image
capture spec.

Fill in one copy of this checklist per client. Save filled-in
artefacts as `docs/mcp/cold-tests/<date>-<client>-<tester>.md` so
future reviewers can see the evidence trail.

## Tester metadata

| Field                    | Value                                 |
| ------------------------ | ------------------------------------- |
| Tester name              |                                       |
| Tester role / team       |                                       |
| Date (UTC)               |                                       |
| OS + version             | (e.g. macOS 15.2, Ubuntu 24.04)       |
| Client name              | Claude Code \| Cursor \| VS Code      |
| Client version           |                                       |
| Cordum gateway version   |                                       |
| Cordum deploy environment| (local dev, staging, prod)            |
| Prior Cordum exposure    | first-time \| familiar \| daily user  |

## Pre-test baseline

- [ ] Fresh client install (no prior MCP config from other projects).
- [ ] `CORDUM_API_KEY` obtained from the admin (step: how long did
      this take?).
- [ ] Elapsed from `git clone` / client download to starting the
      walkthrough: ____ minutes.

## Step-by-step walkthrough

For each step, record the elapsed time, any errors, and whether the
copy-pasteable snippet in the doc worked verbatim or needed editing.

### 1. Install the client

- [ ] Followed the install step from the doc.
- [ ] Client launched successfully.
- Elapsed: ____ s
- Errors / edits / notes:

### 2. Prereqs block

- [ ] Located `CORDUM_API_KEY` per the shared prereqs doc.
- [ ] Confirmed the gateway endpoint URL is reachable
      (`curl -H "X-API-Key: ..." $CORDUM_GATEWAY_URL/api/v1/health`
      returns 200).
- [ ] TLS: self-signed cert trusted per the per-client CA instructions,
      OR the deploy uses a real cert.
- Elapsed: ____ s
- Errors / edits / notes:

### 3. Paste the MCP config snippet

- [ ] Copy-pasted the client-specific `mcp.json` / settings snippet
      from the onboarding doc.
- [ ] Replaced `__YOUR_API_KEY__` placeholder (if any).
- [ ] Saved the config file.
- Elapsed: ____ s
- Errors / edits / notes:

### 4. First tool call — "list my jobs"

- [ ] Opened a chat / agent session in the client.
- [ ] Asked the model: "List my Cordum jobs".
- [ ] Model emitted a `cordum.jobs.list` tool call (visible in the
      client's tool-call UI).
- [ ] Tool call succeeded (200 from the gateway).
- [ ] Model summarised the results in prose.
- Elapsed: ____ s
- Errors / edits / notes:

### 5. Second tool call — policy introspection

- [ ] Asked: "What policies are active?".
- [ ] Model emitted `cordum.policy.list` or similar.
- [ ] Result surfaced in chat.
- Elapsed: ____ s
- Errors / edits / notes:

### 6. First prompt — draft_safety_rule

- [ ] Requested the `draft_safety_rule` prompt with scenario "block
      agents from calling the internal billing API without approval"
      and `risk_level=high`.
- [ ] Client rendered the server-provided system + user messages.
- [ ] Model output contained:
  - [ ] A ` ```yaml ` fenced rule with `match:` + `decision: deny` or
        `require_approval`.
  - [ ] The verbatim simulate-before-apply disclaimer.
  - [ ] A 2-sentence rationale.
- Elapsed: ____ s
- Errors / edits / notes:

### 7. Second prompt — explain_denial

- [ ] Seeded a deny event (run a job with a denied topic, or use the
      staging tenant's canary).
- [ ] Requested `explain_denial` with the denied `job_id`.
- [ ] Model output named the actual rule_id + reason (not hallucinated).
- [ ] Model suggested one of: approval, retry, policy-exception.
- Elapsed: ____ s
- Errors / edits / notes:

## Final verdict

- [ ] **Total elapsed from step 1 to step 7: ____ minutes.**
- [ ] Under 5 minutes? yes / no.
- [ ] Any step required editing the docs? yes / no.
      If yes, list the edits here so docs/cordum maintainers can
      update the public docs:

- [ ] Any step required abandoning the public doc and grepping the
      codebase? yes / no. If yes, which step + what was missing?

## Tester signature

| Field            | Value                          |
| ---------------- | ------------------------------ |
| Tester sign-off  |                                |
| Date (UTC)       |                                |
| Final pass/fail  | pass \| partial \| fail        |

## Reviewer sign-off (second human)

- [ ] Checklist complete, no skipped steps.
- [ ] Tester evidence is consistent (timestamps + screenshots if
      attached).
- [ ] Errors / edits folded into a follow-up docs task if any.

| Field              | Value                         |
| ------------------ | ----------------------------- |
| Reviewer sign-off  |                               |
| Date (UTC)         |                               |
| Follow-up task id  | (if edits were needed)        |
