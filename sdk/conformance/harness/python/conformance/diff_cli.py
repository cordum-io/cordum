"""diff-cli: reads parity scenarios from stdin, emits per-case
verdict JSON on stdout. Used by parity/run.sh to cross-check this
harness's diff against the Go + TypeScript equivalents.

    python -m conformance.diff_cli < parity/scenarios.json > verdicts-python.json
"""

from __future__ import annotations

import json
import sys

from . import _diff


def main() -> int:
    raw = sys.stdin.read()
    scenarios = json.loads(raw)
    verdicts = []
    for s in scenarios:
        name = s.get("name", "")
        want = bool(s.get("want_pass", False))
        try:
            _diff.diff(s.get("actual"), s.get("expected"), "$")
            passed = True
            err = ""
        except _diff.DiffError as exc:
            passed = False
            err = str(exc)
        verdicts.append(
            {
                "name": name,
                "passed": passed,
                "error": err,
                "want_pass": want,
                "agreed_with_want": passed == want,
            }
        )
    json.dump(verdicts, sys.stdout, indent=2)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
