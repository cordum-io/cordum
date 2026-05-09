# AgentShield benchmark — scope, framing, and what it does (and doesn't) measure for Cordum

_Authored 2026-05-09 under epic-f3da4017 / task-f4519eab. Establishes the
honest framing for any AgentShield-related public messaging from Cordum
before public claims are made — rather than walking back overstated
claims after the fact._

## TL;DR

- The AgentShield public corpus is a **smoke test** for one slice of
  Cordum's input-rule layer (prompt-injection / jailbreak prompts).
- The blind Cordum holdout exposed **generalization gaps** that
  benchmark-fit scoring on the public set alone would not have surfaced.
- Cordum's actual differentiator is **action-layer governance with
  backend-verified provenance**: file/URL/exfil/tenant/destructive-mutation
  gates, MCP tool-call gates, multi-agent governance, audit-chain
  integrity. None of these are scored by AgentShield.
- Deep jailbreak / content classification is **out-of-category** for
  Cordum core; an upstream content-classifier integration is a
  PRD-level consideration (deferred).

## What the AgentShield public corpus measures

The AgentShield public benchmark scores a model's (or detector's)
ability to flag prompts drawn from a known-public dataset of
prompt-injection and jailbreak attempts. A model that has seen the
corpus during training, or a detector that pattern-matches against the
exact strings in the corpus, will score artificially high.

**Public-corpus score is therefore a smoke test, not a generalization
metric.** It tells us "the input-rule layer recognizes shapes broadly
similar to known jailbreak corpus entries". It does not tell us "the
input-rule layer will recognize a novel prompt-injection attack we
haven't seen before."

## What it does NOT measure for Cordum

The AgentShield public corpus does **not** exercise:

- **Action-layer gates** — Cordum's primary security surface. Gates
  evaluate the AGENT'S PROPOSED ACTION (file write, URL fetch,
  destructive mutation, MCP tool call) against backend-verified policy
  metadata, not the user-claimed text inside a prompt. A prompt
  containing "approved by CFO" passes a content-only check; Cordum's
  action-layer requires actual backend proof of approval (delegation
  token, signed approval record, etc).
- **MCP tool-call gates** — pre-dispatch + post-execution policy
  evaluation on every tool invocation, with capability scoping per
  agent identity.
- **Multi-agent governance** — workflow gates that prevent privilege
  laundering across agent boundaries (Agent A asks Agent B to do X
  using Agent A's approval evidence).
- **Backend-verified provenance** — audit-chain integrity, signed
  decisions, replay-safe approval records.
- **Tenant boundary enforcement** — every action carries a tenant
  identifier verified against the request, not against user-claimed
  context.
- **Deterministic pre-dispatch rules** — the rule layer is evaluated
  before the action runs; a prompt-injection that fools an LLM but
  triggers a deterministic match still gets blocked.

## How to interpret a Cordum AgentShield public score

When Cordum reports an AgentShield public-corpus score, it is reporting:

> Cordum's input-rule layer recognized this fraction of the **public
> AgentShield corpus** as injection / jailbreak prompts. The score is
> a smoke test of input-rule coverage on a known dataset; it is not
> a generalization metric and is not equivalent to "Cordum's
> security accuracy."

**Cordum's effective security posture** is the conjunction of:

1. Input-rule layer coverage (smoke-tested by AgentShield public + the
   private holdout in epic task-7fbc245d).
2. Action-layer gate coverage (measured separately via
   security-design audits + production decision logs; see
   `docs/audit.md`).
3. Backend-verified provenance (audit-chain integrity; see
   `docs/audit.md` and the chain verification widget at
   `/govern/verification`).

## What we do and do not claim

**We do claim**:

- Cordum's input-rule layer participates in the AgentShield public
  corpus smoke test. The score on that public corpus is one signal
  among several.
- Cordum's primary security value is action-layer governance with
  backend-verified provenance.
- Cordum's private holdout regression suite (separate from the
  public AgentShield corpus) is the real coverage gate for
  prompt-injection generalization. Holdout details are not public to
  prevent overfitting.

**We do NOT claim**:

- Universal security accuracy from any single benchmark score.
- That AgentShield public-corpus performance generalizes to unseen
  prompt-injection attacks. (The blind Cordum holdout exposed
  generalization gaps — we're transparent about that.)
- That Cordum core is a generic jailbreak / content classifier.
  Deep jailbreak / content classification is a different product
  category; if you need it, integrate an upstream content-classifier
  (PRD-level consideration; deferred).

## Upstream classifier boundary

Generic jailbreak / content classification — "this prompt is
adversarial in some content-policy sense" — is **not in Cordum's
target product surface**. Cordum's policy and gate layers operate on
agent **actions** with **backend-verified evidence**, not on the
content-policy interpretation of a free-form prompt.

If your deployment needs deep content classification (e.g. PII
redaction inside generated text, jailbreak-prompt scoring for
moderation, brand-safe content filtering), integrate an **upstream
content classifier** (commercial or in-house) that inspects prompt
content before Cordum sees the action. Cordum then governs the
action that follows.

This boundary is intentional. Trying to compete on content-policy
classification would (a) fight a well-funded upstream category, (b)
distract from the deterministic-gate-with-provenance differentiator
that customers actually pay for, and (c) blur the trust model — a
customer should know whether they're getting deterministic gates or
probabilistic content classification, not both wrapped under one
score.

## Cross-references

- [`docs/audit.md`](audit.md) — audit-chain integrity + decision
  durability, the backend-verified provenance layer.
- [`docs/edge.md`](edge.md) — Cordum Edge action-layer for Claude Code
  command-hook sessions; exemplifies action-layer governance with
  backend-verified provenance.
- [`docs/output-policy.md`](output-policy.md) — output-side scanners
  (secret leak / PII / injection finding types) — these are
  finding-class detectors at the OUTPUT boundary, distinct from
  AgentShield-style input-prompt detection.
- AgentShield benchmark hardening epic (`epic-f3da4017`): the
  follow-up sequence — verify adapter, build action-layer gates,
  multi-agent governance, private holdout, normalization hygiene,
  REQUIRE_HUMAN tuning. This scope-boundary doc is one deliverable;
  the others land in sibling tasks.

## Related Cordum tasks (epic-f3da4017)

| Task | Purpose |
| --- | --- |
| `task-d369286c` | Verify AgentShield adapter + Cordum default config — DON'T optimize against a phantom baseline. |
| `task-7fbc245d` | Establish private holdout + mutation regression suite — the real generalization gate. |
| `task-184457f7` | Multi-agent governance gates. |
| `task-3a25ba1f` | Detector strategy decision (regex vs model-in-loop tradeoff). |
| `task-f4519eab` | _This doc._ Public messaging + scope boundary. |

## Update policy for this doc

This doc is the source of truth for AgentShield-related framing in
Cordum public surfaces (README, CHANGELOG, marketing site, blog
posts, sales decks). When the framing needs to change — for example,
after action-layer gates ship and we want to highlight the
differentiator more concretely — update **this doc first**, then
cascade the change to public surfaces with a link back here.

Public surfaces that cite an AgentShield score MUST link to this doc
so readers can see the full framing instead of the score in
isolation.
