# LLM data-leakage governance demo

**Scenario:** an AI agent that sits in on meetings and produces summaries +
action items, built on **LangChain/LangGraph + the OpenAI API**. Meeting
content is some of the most sensitive data a company has — names, comp, customer
details, strategy, the occasional secret pasted into notes — and all of it flows
to a third-party model API.

**What this demo shows:** Cordum puts a governance checkpoint in front of
OpenAI so **every prompt and response is redacted and audited before it can
leave your boundary** — with no change to the agent beyond pointing its OpenAI
client at the Cordum proxy.

```
  meeting agent ──/v1/chat/completions──▶  Cordum proxy  ──┬──▶ Cordum gateway
  (LangChain ChatOpenAI,                                   │     POST /api/v1/edge/llm/events
   base_url = proxy)                                       │     • redact via the SHARED
                                                           │       Safety Kernel scanners
                                                           │     • immutable audit (layer=llm)
                                                           │
                                                           └──▶ OpenAI  ← receives the
                                                                          REDACTED payload only
```

The proxy invents no redaction logic. Detection happens **once**, in Cordum's
control plane (the Safety Kernel scanners), and the edge/LLM path reuses it —
one source of truth for the whole platform.

## The three beats

1. **Raw meeting transcript** (`fixtures/meeting_transcript.txt`) — contains
   names, emails, a phone number, a payment card, a comp figure, a project
   codename, and an accidentally-pasted API key.
2. **What OpenAI actually receives** — the proxy prints the redacted payload;
   every sensitive span is masked in place (`<redacted:pii>`,
   `<redacted:secret_leak>`, `<redacted:keyword_match>`, `<redacted:custom>`),
   while the rest of the discussion is preserved so the summary still works.
3. **Proof for the auditor** — the turn appears in the Edge audit trail
   (`/api/v1/edge/sessions/<id>/events`) as a `layer=llm` event with
   `llm.finding.*` labels and **only redacted** input stored — never the raw
   values.

## What gets redacted (honest)

| In the transcript | Caught by | Mechanism |
|---|---|---|
| emails, phone, payment card (Luhn) | built-in **PII scanner** | `safetykernel` `piiScanner` |
| pasted `sk-…` API key | built-in **secret scanner** | `safetykernel` `secretScanner` |
| salary / comp figure (`$185,000`) | **custom pattern** (tenant config) | kernel regex scanner |
| employee names, project codename, company | **keyword roster** (tenant config) | kernel keyword scanner |

These are **regex + Luhn + keyword** detectors — fast, deterministic, in-path.
They will **not** catch an arbitrary, un-rostered person name in free prose
(e.g. someone mentioned once). That requires contextual NER/ML, which plugs into
the same `OutputScanner` interface (Microsoft Presidio / AWS Comprehend / GCP
DLP) — see *Roadmap* below. We show only redactions that genuinely happen.

## Run it

```bash
# 0) Cordum stack up (from repo root):  ./tools/scripts/quickstart.sh
# 1) Enable the demo on the gateway (PII+secret+keyword+custom redaction):
./setup.sh --apply           # appends config/gateway.demo.env to .env, restarts gateway

# 2) Install proxy + agent deps:
pip install -r proxy/requirements.txt -r agent/requirements.txt

# 3) Run end-to-end (starts proxy, runs the agent, prints the audit trail):
./run_demo.sh
```

Defaults run **fully offline** with a mock upstream — no data is sent to OpenAI
during the demo (important when the whole point is preventing data egress). To
forward to the real API: `UPSTREAM=openai OPENAI_API_KEY=sk-… ./run_demo.sh`.

The single integration change for any LangChain app:

```python
ChatOpenAI(model="gpt-4o-mini", base_url="http://localhost:8088/v1", api_key=...)
```

## Verified end-to-end (Docker)

Run against the local dev stack, one meeting transcript through the proxy:
- **Egress:** none of the raw values (names, emails, phone, card, comp, codename, `sk-` key) reached the model.
- **Audit:** the turn is recorded as `layer=llm`, `decision=RECORDED`, with `llm.finding.{pii,secret_leak,keyword_match,custom}` labels and **only redacted** input stored — e.g. `OPENAI_API_KEY=<redacted:secret_leak>`.

## Gotchas (from the E2E run)

- **Two keys.** Creating the edge session/execution needs an `admin`/`user` role; ingest needs the `llm_proxy` role. The proxy uses `CORDUM_BOOTSTRAP_API_KEY` for the former and `CORDUM_API_KEY` for the latter.
- **RBAC stacks:** if RBAC is entitled, create the role once — `PUT /api/v1/auth/roles/llm_proxy {"permissions":["edge.llm.ingest"]}` (`setup.sh` does this).
- **Container env:** the gateway must receive `CORDUM_EDGE_LLM_*` + `CORDUM_API_KEYS` in its *container* environment (compose `environment:`/override), not just a root `.env` the compose file doesn't map.
- **Local TLS:** the dev gateway cert has no `localhost` SAN → set `CORDUM_TLS_INSECURE=true` for the proxy (run_demo.sh does this).

## Configuration

Gateway-side (`config/gateway.demo.env`, read by the gateway via
`safetykernel.ContentScanOptionsFromEnv`):

- `CORDUM_EDGE_LLM_INGEST_ENABLED=true` — expose the endpoint
- `CORDUM_EDGE_LLM_DETECT_PII` / `_DETECT_SECRETS` — built-in scanners (default on)
- `CORDUM_EDGE_LLM_REDACT_KEYWORDS` — org roster / codenames (comma-separated)
- `CORDUM_EDGE_LLM_REDACT_PATTERNS` — org custom regexes (JSON `[{"name","pattern"}]`)

## How it maps to the platform

- **Detection = the Safety Kernel scanners** (`core/controlplane/safetykernel/scanners.go`,
  exposed via `RedactContent`/`ScanContent` in `content_redact.go`). Edge never
  re-implements detection; the gateway **injects** the kernel redactor into the
  `core/edge/llmingest` adapter — edge and control plane share one system.
- **Ingest + audit** = `POST /api/v1/edge/llm/events` (`llm-proxy` execution
  adapter, tenant-scoped). See `docs/edge/llm-proxy-governance.md`.
- **Blocking a prompt/response** (deny, not just redact) reuses the
  layer-agnostic `POST /api/v1/edge/evaluate` (the safety kernel decision).

## Roadmap (to close the regex gap)

Contextual / ML PII for arbitrary names and entities plugs in behind the
existing `OutputScanner` interface — Microsoft Presidio, AWS Comprehend, or GCP
DLP as an additional scanner — with **no change** to the proxy or the edge path,
because detection is centralized in the kernel.
