"""Baseline LangGraph agent — NO Cordum safety layer.

This agent has 3 tools:
  - lookup_balance: safe read-only operation
  - transfer_funds: risky financial operation
  - delete_account: destructive operation

Run:  python agent.py
"""

from __future__ import annotations

import os

from langchain_core.tools import tool
from langchain_openai import ChatOpenAI
from langgraph.prebuilt import create_react_agent


# ---------------------------------------------------------------------------
# Tools — a simulated banking API
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
# Agent
# ---------------------------------------------------------------------------

def main() -> None:
    llm = ChatOpenAI(
        model=os.getenv("OPENAI_MODEL", "gpt-4o-mini"),
        temperature=0,
    )

    tools = [lookup_balance, transfer_funds, delete_account]
    agent = create_react_agent(llm, tools)

    print("=" * 60)
    print("  Banking Agent (NO safety layer)")
    print("=" * 60)

    queries = [
        "What is Alice's account balance?",
        "Transfer $15,000 from Bob to Alice.",
        "Delete Charlie's account.",
    ]

    for query in queries:
        print(f"\n> {query}")
        result = agent.invoke({"messages": [("human", query)]})
        last_msg = result["messages"][-1]
        print(f"  {last_msg.content}")


if __name__ == "__main__":
    main()
