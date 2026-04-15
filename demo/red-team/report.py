"""Red-team report formatting."""

from __future__ import annotations

import datetime
from dataclasses import dataclass


@dataclass
class ScenarioResult:
    id: int
    tier: str
    name: str
    description: str
    expected: str
    actual: str
    governance_held: bool  # True = Cordum blocked the attack
    detail: str = ""


def print_report(results: list[ScenarioResult]) -> None:
    now = datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%d %H:%M UTC")
    width = 72

    held = sum(1 for r in results if r.governance_held)
    bypassed = sum(1 for r in results if not r.governance_held)
    total = len(results)

    print()
    print("=" * width)
    print(f"  CORDUM RED TEAM REPORT -- {now}")
    print("=" * width)

    current_tier = ""
    for r in results:
        if r.tier != current_tier:
            current_tier = r.tier
            print()
            print(f"  {current_tier}")
            print(f"  {'-' * (width - 4)}")

        tag = "HELD" if r.governance_held else "BYPASS"
        icon = " OK " if r.governance_held else "FAIL"
        print(f"  [{icon}] #{r.id:>2} {r.name}")
        print(f"         Expected: {r.expected}")
        print(f"         Actual:   {r.actual}")
        if r.detail:
            for line in r.detail.splitlines():
                print(f"         | {line}")

    print()
    print("-" * width)
    print(f"  SUMMARY: {held}/{total} attacks blocked, {bypassed} bypasses found")
    if bypassed > 0:
        print()
        print("  BYPASSES:")
        for r in results:
            if not r.governance_held:
                print(f"    #{r.id} {r.name}")
    print("=" * width)
    print()
