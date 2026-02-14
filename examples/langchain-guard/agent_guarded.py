"""LangGraph agent WITH Cordum safety layer.

Same 3 tools as agent.py, but wrapped through CordumToolGuard.
Cordum's Safety Kernel evaluates each tool call before execution:
  - lookup_balance  → ALLOW  (safe, read-only)
  - transfer_funds  → REQUIRE_APPROVAL if amount > $10,000
  - delete_account  → DENY   (destructive operations blocked)

Prerequisites:
  1. Cordum stack running:  docker compose up -d
  2. API key set:           export CORDUM_API_KEY=<your-key>
  3. Safety policy loaded:  see safety.yaml in this directory

Run:  python agent_guarded.py
"""

from __future__ import annotations

import os
import sys

from langchain_core.tools import tool
from langchain_openai import ChatOpenAI
from langgraph.prebuilt import create_react_agent

from cordum_guard import CordumClient
from cordum_guard.langchain import CordumToolGuard


# ---------------------------------------------------------------------------
# Tools — same simulated banking API as agent.py
# ---------------------------------------------------------------------------

ACCOUNTS: dict[str, float] = {
    "alice": 5_200.00,
    "bob": 12_800.50,
    "charlie": 340.00,
}


@tool
def lookup_balance(account: str) -> str:
    """Look up the current balance for an account."""
    bal = ACCOUNTS.get(account.lower())
    if bal is None:
        return f"Account '{account}' not found."
    return f"Account '{account}' balance: ${bal:,.2f}"


@tool
def transfer_funds(from_account: str, to_account: str, amount: float) -> str:
    """Transfer funds between two accounts."""
    src = from_account.lower()
    dst = to_account.lower()
    if src not in ACCOUNTS:
        return f"Source account '{src}' not found."
    if dst not in ACCOUNTS:
        return f"Destination account '{dst}' not found."
    if ACCOUNTS[src] < amount:
        return f"Insufficient funds in '{src}'."
    ACCOUNTS[src] -= amount
    ACCOUNTS[dst] += amount
    return f"Transferred ${amount:,.2f} from {src} to {dst}. New balances: {src}=${ACCOUNTS[src]:,.2f}, {dst}=${ACCOUNTS[dst]:,.2f}"


@tool
def delete_account(account: str) -> str:
    """Permanently delete an account and all its data."""
    acct = account.lower()
    if acct not in ACCOUNTS:
        return f"Account '{acct}' not found."
    del ACCOUNTS[acct]
    return f"Account '{acct}' has been permanently deleted."


# ---------------------------------------------------------------------------
# Cordum safety guard
# ---------------------------------------------------------------------------

def build_guarded_tools() -> list:
    api_key = os.getenv("CORDUM_API_KEY")
    if not api_key:
        print("ERROR: Set CORDUM_API_KEY environment variable.")
        sys.exit(1)

    gateway_url = os.getenv("CORDUM_GATEWAY_URL", "http://localhost:8081")

    client = CordumClient(gateway_url, api_key=api_key)
    guard = CordumToolGuard(
        client,
        policy="financial_ops",
        risk_tags=["financial"],
    )

    tools = [lookup_balance, transfer_funds, delete_account]
    safe_tools = guard.wrap(tools)

    print(f"[CORDUM] Connected to {gateway_url}")
    print(f"[CORDUM] Guarded {len(safe_tools)} tools: {[t.name for t in safe_tools]}")
    return safe_tools


# ---------------------------------------------------------------------------
# Agent
# ---------------------------------------------------------------------------

def main() -> None:
    llm = ChatOpenAI(
        model=os.getenv("OPENAI_MODEL", "gpt-4o-mini"),
        temperature=0,
    )

    safe_tools = build_guarded_tools()
    agent = create_react_agent(llm, safe_tools)

    print()
    print("=" * 60)
    print("  Banking Agent (with Cordum safety layer)")
    print("=" * 60)

    queries = [
        # Safe: read-only lookup → ALLOW
        "What is Alice's account balance?",
        # Risky: large transfer → REQUIRE_APPROVAL
        "Transfer $15,000 from Bob to Alice.",
        # Destructive: delete → DENY
        "Delete Charlie's account.",
    ]

    for query in queries:
        print(f"\n> {query}")
        result = agent.invoke({"messages": [("human", query)]})
        last_msg = result["messages"][-1]
        print(f"  {last_msg.content}")


if __name__ == "__main__":
    main()
