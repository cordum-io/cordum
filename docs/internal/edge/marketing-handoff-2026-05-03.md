# Cordum Edge P0 marketing handoff (2026-05-03)

**Status:** TODO for the marketing team. Handed off 2026-05-03 from
EDGE-048 (cordum repo doc visibility sweep).

**Repo scope:** the items below all live in the `Coretex-site/` repo
(cordum.io marketing site, TypeScript Next.js 16). They are intentionally
out of scope for the cordum core repo — this file documents the handoff
so it does not fall through.

The cordum-side product surface for Cordum Edge is complete:
- Reference docs under `docs/edge/` (12 files: api, configuration, cli,
  cordum-agentd, cordum-hook, cordumctl-edge-claude, cordumctl-edge-doctor,
  demo, managed-settings-template, runbook, README, claude-hook-mapper).
- 30-minute new-engineer walkthrough at `docs/quickstart-edge.md`.
- README.md surface (hero sub-tagline, Edge Quickstart section, Key
  Features bullet, Compared To row).
- AGENTS.md "Cordum Edge" section for AI agents reading the codebase.
- CHANGELOG.md "Cordum Edge P0 (2026-04-30)" + "Cordum Edge P0 cleanup
  (2026-05-03)" sections.
- docs/system_overview.md "Cordum Edge" bullet under Core components.

## Handoff items

### 1. `/products/edge` landing page — DRAFT

A dedicated product page under cordum.io. Suggested H1: **Cordum Edge —
Compliance Firewall for AI Agent Actions**.

DRAFT outline (marketing team to refine voice, screenshots, CTAs):

- Hero: "Know what your AI agents are about to do, before they do it."
  Sub-headline naming Claude Code as the day-1 integration with extension
  to other agent hook contracts as roadmap.
- Three-pane "what gets governed": deny risky reads (e.g. `.env`),
  approval-gate edits, allow safe reads + builds + tests.
- Architecture explainer (one diagram): cordum-hook → cordum-agentd →
  Gateway `/api/v1/edge/*` → Safety Kernel evaluate → approvals/audit.
- Evidence story: per-session redacted bundle export, dashboard timeline,
  artifact pointers.
- Wrapper-vs-enterprise: developer wrapper is `cordumctl edge claude`;
  enterprise enforcement uses managed Claude settings.
- CTAs: docs.cordum.io/quickstart-edge, GitHub repo, Discord.

### 2. `/blog/launching-cordum-edge` announcement post — DRAFT

Standalone blog post on the Coretex-site blog. Suggested angle: technical
launch story rather than marketing pitch (cordum.io audience is
governance-engineering practitioners).

Draft sections (marketing team to write):

- Why a Compliance Firewall for AI agents (the tool-call gap between LLM
  governance and infra governance).
- How Claude Code's command hook contract gives us a standard intercept
  point that doesn't require running Claude Code in a sandbox.
- The three-step deny / approve / consume flow with a concrete example.
- Day-1 limits: developer wrapper only; enterprise needs managed settings.
- What's next: extending the same hook → agentd → evaluate → audit shape
  to other agents that expose hook contracts.

### 3. Homepage copy update

Current homepage hero copy is platform-wide. Add a sub-tagline mirroring
the cordum repo's README hero sub-tagline:

> Includes Cordum Edge — a Compliance Firewall for Claude Code and other
> local AI-agent actions.

Or replace the "products" carousel slide with an Edge-first slide if the
homepage uses one.

### 4. Navigation update

Add an Edge entry under the site's primary "Products" navigation:

- Products → Cordum Edge → `/products/edge`

If the site does not yet have a Products mega-menu, add one with Edge as
the first entry; subsequent products (MCP zero-trust, etc.) follow as
they ship.

## Coordination

If the marketing team needs technical review on copy:
- Ping #general or #marketing on Discord.
- Or open an issue against the `Coretex-site` GitHub repo and tag a
  cordum maintainer for accuracy review before publish.

If the marketing team needs new screenshots:
- Run `make dev-up` from the cordum repo, follow `docs/quickstart-edge.md`,
  then capture the dashboard `/edge/sessions` list page and a single
  session detail timeline. Use synthetic prompts — no real customer data.

## Out of scope for this handoff

- Customer testimonials or case studies (no design partners under NDA
  for Edge yet at handoff time).
- Pricing / SKU page changes (Cordum Edge is part of the platform,
  not a separate SKU at P0).
- Sales decks, webinar collateral, conference talks (separate marketing
  workstreams).
