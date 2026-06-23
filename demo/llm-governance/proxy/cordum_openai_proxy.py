"""Cordum OpenAI governance proxy.

A drop-in, OpenAI-compatible endpoint that sits between an AI agent (e.g. a
LangChain/LangGraph meeting assistant) and OpenAI. Every chat turn is routed
through Cordum's control plane BEFORE it reaches the model provider:

    agent ──/v1/chat/completions──▶ THIS PROXY ──┬─▶ Cordum gateway
                                                  │     /api/v1/edge/llm/events
                                                  │     (redact via the SHARED
                                                  │      Safety Kernel scanners
                                                  │      + immutable audit)
                                                  └─▶ OpenAI  (REDACTED payload)

The proxy never invents redaction logic — the gateway does it, using Cordum's
Safety Kernel scanners (PII + secrets + operator terms). The proxy only swaps
the outbound message content for the gateway-redacted version, so secrets and
PII never leave your boundary, and the entire turn lands in the Edge audit trail.

The default upstream is an offline MOCK that echoes exactly what it received —
so a data-leakage demo never actually ships data to OpenAI. Set UPSTREAM=openai
(+ OPENAI_API_KEY) to forward to the real API.

Run:
    pip install -r requirements.txt
    uvicorn cordum_openai_proxy:app --port 8088

Env:
    CORDUM_GATEWAY        gateway base URL           (default https://localhost:8081)
    CORDUM_API_KEY        proxy API key (role=llm_proxy / perm edge.llm.ingest)
    CORDUM_TENANT         tenant id                  (default "default")
    CORDUM_PROXY_PRINCIPAL the principal_id bound to the API key (== source_id)
    CORDUM_CA_CERT        path to the gateway CA cert (default ./certs/ca/ca.crt)
    UPSTREAM              "mock" | "openai"          (default "mock")
    OPENAI_API_KEY        upstream key (only for UPSTREAM=openai)
    OPENAI_BASE           upstream base URL          (default https://api.openai.com/v1)
"""
from __future__ import annotations

import os
import time
import uuid
from typing import Any

import httpx
from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse

GATEWAY = os.getenv("CORDUM_GATEWAY", "https://localhost:8081").rstrip("/")
API_KEY = os.getenv("CORDUM_API_KEY", "")
TENANT = os.getenv("CORDUM_TENANT", "default")
PRINCIPAL = os.getenv("CORDUM_PROXY_PRINCIPAL", "llm-proxy-1")
CA_CERT = os.getenv("CORDUM_CA_CERT", "./certs/ca/ca.crt")
UPSTREAM = os.getenv("UPSTREAM", "mock").lower()
OPENAI_API_KEY = os.getenv("OPENAI_API_KEY", "")
OPENAI_BASE = os.getenv("OPENAI_BASE", "https://api.openai.com/v1").rstrip("/")

# verify: path to CA bundle when present, else skip (demo-only).
_VERIFY: Any = CA_CERT if os.path.exists(CA_CERT) else False

app = FastAPI(title="Cordum OpenAI governance proxy")

_state: dict[str, str] = {}  # session_id / execution_id, created at startup


def _gw_headers() -> dict[str, str]:
    return {
        "X-API-Key": API_KEY,
        "X-Tenant-ID": TENANT,
        "Content-Type": "application/json",
    }


def _bootstrap_session() -> None:
    """Create the Edge session + llm-proxy execution this proxy records into.

    The proxy is shared infrastructure: it owns an `llm-proxy` execution within
    the tenant (not bound to any one developer's principal).
    """
    with httpx.Client(verify=_VERIFY, timeout=10.0) as c:
        sess = c.post(
            f"{GATEWAY}/api/v1/edge/sessions",
            headers=_gw_headers(),
            json={
                "agent_product": "meeting-assistant",
                "agent_version": "demo",
                "mode": "enterprise-managed",
                "policy_snapshot": "demo",
                "policy_mode": "enforce",
            },
        )
        sess.raise_for_status()
        session_id = sess.json()["session_id"]
        exe = c.post(
            f"{GATEWAY}/api/v1/edge/executions",
            headers=_gw_headers(),
            json={
                "session_id": session_id,
                "adapter": "llm-proxy",
                "mode": "enterprise-managed",
            },
        )
        exe.raise_for_status()
        _state["session_id"] = session_id
        _state["execution_id"] = exe.json()["execution_id"]
    print(f"[cordum-proxy] session={_state['session_id']} execution={_state['execution_id']}")


@app.on_event("startup")
def _startup() -> None:
    if not API_KEY:
        print("[cordum-proxy] WARNING: CORDUM_API_KEY not set")
    try:
        _bootstrap_session()
    except Exception as exc:  # noqa: BLE001 - surface bootstrap failure clearly
        print(f"[cordum-proxy] FAILED to create edge session/execution: {exc}")
        print("[cordum-proxy] is the gateway up with CORDUM_EDGE_LLM_INGEST_ENABLED=true?")


def _ingest(events: list[dict[str, Any]]) -> list[dict[str, Any]]:
    """POST a batch of LLM events to the gateway; return per-event decisions."""
    body = {"source": {"source_id": PRINCIPAL}, "events": events}
    with httpx.Client(verify=_VERIFY, timeout=15.0) as c:
        resp = c.post(f"{GATEWAY}/api/v1/edge/llm/events", headers=_gw_headers(), json=body)
        resp.raise_for_status()
        return resp.json().get("decisions", [])


def _event(kind: str, direction: str, content: str, seid: str) -> dict[str, Any]:
    return {
        "tenant_id": TENANT,
        "session_id": _state.get("session_id", ""),
        "execution_id": _state.get("execution_id", ""),
        "source_event_id": seid,
        "observed_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "kind": kind,
        "provider": "openai",
        "direction": direction,
    } | ({"content": content} if content else {})


def _redact_messages(messages: list[dict[str, Any]], turn: str) -> tuple[list[dict[str, Any]], list[str]]:
    """Send each message through Cordum; return messages with redacted content."""
    events = [
        _event("llm.request.pre", "prompt", str(m.get("content", "")), f"{turn}-msg-{i}")
        for i, m in enumerate(messages)
        if str(m.get("content", "")).strip()
    ]
    if not events:
        return messages, []
    decisions = _ingest(events)
    by_seid = {d["source_event_id"]: d for d in decisions}
    out, findings = [], []
    di = 0
    for i, m in enumerate(messages):
        content = str(m.get("content", ""))
        d = by_seid.get(f"{turn}-msg-{i}")
        if d and content.strip():
            if d.get("decision") == "redact" and d.get("redacted_content"):
                content = d["redacted_content"]
            findings += d.get("findings", [])
        out.append({**m, "content": content})
        di += 1
    return out, sorted(set(findings))


def _mock_completion(model: str, redacted_messages: list[dict[str, Any]]) -> dict[str, Any]:
    last = next((m["content"] for m in reversed(redacted_messages) if m.get("role") == "user"), "")
    seen = " | ".join(f"{m.get('role')}: {m.get('content','')[:120]}" for m in redacted_messages)
    text = (
        "[mock-openai] I only ever received the REDACTED payload below — no raw "
        "names, emails, comp figures, or secrets reached the model:\n  "
        + seen[:600]
        + "\n\n(Action items would be generated here from the redacted transcript.)"
    )
    return {
        "id": f"chatcmpl-mock-{uuid.uuid4().hex[:12]}",
        "object": "chat.completion",
        "created": int(time.time()),
        "model": model,
        "choices": [{"index": 0, "message": {"role": "assistant", "content": text}, "finish_reason": "stop"}],
        "usage": {"prompt_tokens": len(last) // 4, "completion_tokens": 64, "total_tokens": len(last) // 4 + 64},
    }


def _real_completion(body: dict[str, Any], redacted_messages: list[dict[str, Any]]) -> dict[str, Any]:
    payload = {**body, "messages": redacted_messages}
    with httpx.Client(timeout=60.0) as c:
        r = c.post(
            f"{OPENAI_BASE}/chat/completions",
            headers={"Authorization": f"Bearer {OPENAI_API_KEY}", "Content-Type": "application/json"},
            json=payload,
        )
        r.raise_for_status()
        return r.json()


@app.post("/v1/chat/completions")
async def chat_completions(request: Request) -> JSONResponse:
    body = await request.json()
    model = body.get("model", "gpt-4o-mini")
    messages = body.get("messages", [])
    turn = uuid.uuid4().hex[:8]

    redacted_messages, in_findings = _redact_messages(messages, turn)

    print(f"\n[cordum-proxy] turn={turn} model={model}")
    print(f"  prompt findings: {in_findings or 'none'}")
    if in_findings:
        print("  → OpenAI will receive the REDACTED prompt (sensitive spans masked)")

    if UPSTREAM == "openai" and OPENAI_API_KEY:
        completion = _real_completion(body, redacted_messages)
    else:
        completion = _mock_completion(model, redacted_messages)

    # Egress governance: audit (and redact) the model's response too.
    answer = completion.get("choices", [{}])[0].get("message", {}).get("content", "")
    if answer:
        out_decisions = _ingest([_event("llm.request.post", "response", answer, f"{turn}-resp")])
        if out_decisions:
            od = out_decisions[0]
            if od.get("decision") == "redact" and od.get("redacted_content"):
                completion["choices"][0]["message"]["content"] = od["redacted_content"]
            if od.get("findings"):
                print(f"  response findings (redacted before return): {od['findings']}")

    return JSONResponse(completion)


@app.get("/healthz")
def healthz() -> dict[str, Any]:
    return {"ok": True, "gateway": GATEWAY, "upstream": UPSTREAM, **_state}
