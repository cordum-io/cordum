"""Sample meeting-assistant agent (LangChain) — governed by Cordum.

This stands in for a production AI agent that ingests meeting transcripts and
produces summaries + action items using OpenAI. The ONLY change needed to
govern it is pointing the OpenAI client's base_url at the Cordum proxy — the
agent code is otherwise unchanged. Every prompt is then redacted + audited by
Cordum's control plane before it can reach OpenAI.

Run (after the proxy is up on :8088):
    pip install -r requirements.txt
    python meeting_agent.py ../fixtures/meeting_transcript.txt

Plain OpenAI SDK equivalent (no LangChain) — same idea:
    from openai import OpenAI
    client = OpenAI(base_url="http://localhost:8088/v1", api_key="not-used-by-mock")
    client.chat.completions.create(model="gpt-4o-mini", messages=[...])
"""
from __future__ import annotations

import os
import sys

from langchain_openai import ChatOpenAI

PROXY = os.getenv("CORDUM_PROXY_URL", "http://localhost:8088/v1")

SYSTEM = (
    "You are a meeting assistant. Summarize the meeting and list concrete "
    "action items with owners and due dates."
)


def main() -> None:
    path = sys.argv[1] if len(sys.argv) > 1 else "../fixtures/meeting_transcript.txt"
    with open(path, encoding="utf-8") as fh:
        transcript = fh.read()

    # The one line that puts Cordum in the path: base_url -> the governance proxy.
    llm = ChatOpenAI(
        model=os.getenv("OPENAI_MODEL", "gpt-4o-mini"),
        base_url=PROXY,
        api_key=os.getenv("OPENAI_API_KEY", "not-used-by-mock"),
        temperature=0,
    )

    print("=" * 70)
    print("  Meeting assistant agent  →  Cordum proxy  →  OpenAI")
    print("=" * 70)
    print(f"  proxy: {PROXY}")
    print(f"  transcript: {path} ({len(transcript)} chars, contains PII + a secret)")
    print()

    resp = llm.invoke(
        [
            ("system", SYSTEM),
            ("human", f"Here is the meeting transcript:\n\n{transcript}"),
        ]
    )

    print("--- model response (what the agent gets back) ---")
    print(resp.content)
    print()
    print("Note: with the default mock upstream, the response echoes exactly the")
    print("REDACTED payload OpenAI received — proving no raw PII/secrets left the")
    print("boundary. Check the Cordum dashboard / audit for the recorded turn.")


if __name__ == "__main__":
    main()
