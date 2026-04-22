"""CLI entrypoint for the Python conformance harness.

Example::

    python -m conformance \\
        --fixtures ../../fixtures \\
        --sim-bin ../../simulator/bin/cordum-gateway-sim \\
        --report ../../reports/python.xml
"""

from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path

from .driver import Driver, Fixture
from ._report import TestCase, TestSuite, write_report

DEFAULT_API_KEY = "conformance-api-key"
DEFAULT_TENANT = "default"


def main() -> int:
    args = _parse_args(sys.argv[1:])
    try:
        proc, base_url = _spawn_simulator(args.sim_bin)
    except Exception as exc:  # noqa: BLE001 — top-level trap
        print(f"harness-python: failed to start simulator: {exc}", file=sys.stderr)
        return 2
    try:
        _wait_ready(base_url, timeout_sec=5.0)
    except RuntimeError as exc:
        print(f"harness-python: simulator never became ready: {exc}", file=sys.stderr)
        _terminate(proc)
        return 2

    driver = Driver(base_url=base_url, api_key=DEFAULT_API_KEY, tenant=DEFAULT_TENANT)
    suite = TestSuite(name="conformance-python")
    pass_count, fail_count = 0, 0
    for fx_path in _walk_fixtures(args.fixtures):
        with open(fx_path, "r", encoding="utf-8") as f:
            data = json.load(f)
        fx = Fixture.from_dict(data)
        start = time.perf_counter()
        tc = TestCase(name=fx.name, class_name=fx.name, time_sec=0.0)
        try:
            driver.run_fixture(fx)
            pass_count += 1
            print(f"PASS {fx.name:<50} ({time.perf_counter() - start:.3f}s)", file=sys.stderr)
        except Exception as exc:  # noqa: BLE001
            tc.failure_message = str(exc)
            fail_count += 1
            print(f"FAIL {fx.name:<50} {exc}", file=sys.stderr)
        tc.time_sec = time.perf_counter() - start
        suite.cases.append(tc)

    write_report(args.report, suite)
    print(
        f"\nharness-python: {pass_count} pass, {fail_count} fail — report={args.report}",
        file=sys.stderr,
    )
    _terminate(proc)
    return 0 if fail_count == 0 else 1


def _parse_args(argv: list[str]) -> argparse.Namespace:
    p = argparse.ArgumentParser(prog="conformance")
    p.add_argument("--fixtures", default="../../fixtures")
    p.add_argument("--sim-bin", default="../../simulator/bin/cordum-gateway-sim")
    p.add_argument("--report", default="../../reports/python.xml")
    return p.parse_args(argv)


def _spawn_simulator(bin_path: str) -> tuple[subprocess.Popen[bytes], str]:
    if not os.path.exists(bin_path):
        raise FileNotFoundError(f"simulator binary not found at {bin_path}")
    proc = subprocess.Popen(
        [bin_path],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    assert proc.stdout is not None
    url_line = proc.stdout.readline().decode("utf-8").strip()
    if not url_line.startswith("http://") and not url_line.startswith("https://"):
        raise RuntimeError(f"simulator did not print URL line; got: {url_line!r}")
    # Drain remaining stdout in background so the child doesn't block
    # on a full pipe buffer.
    import threading

    def _drain(pipe):
        for _ in iter(lambda: pipe.readline(), b""):
            pass

    threading.Thread(target=_drain, args=(proc.stdout,), daemon=True).start()
    threading.Thread(target=_drain, args=(proc.stderr,), daemon=True).start()
    return proc, url_line


def _wait_ready(base_url: str, timeout_sec: float) -> None:
    deadline = time.monotonic() + timeout_sec
    last_err = ""
    while time.monotonic() < deadline:
        try:
            req = urllib.request.Request(base_url.rstrip("/") + "/healthz", method="GET")
            with urllib.request.urlopen(req, timeout=1.0) as resp:
                if resp.status == 200:
                    return
        except urllib.error.URLError as exc:
            last_err = str(exc)
        except Exception as exc:  # noqa: BLE001
            last_err = str(exc)
        time.sleep(0.05)
    raise RuntimeError(last_err or "timeout")


def _walk_fixtures(root: str) -> list[str]:
    out: list[str] = []
    for p in sorted(Path(root).rglob("*.json")):
        out.append(str(p))
    return out


def _terminate(proc: subprocess.Popen[bytes]) -> None:
    if proc.poll() is not None:
        return
    try:
        proc.terminate()
        proc.wait(timeout=2)
    except subprocess.TimeoutExpired:
        proc.kill()


if __name__ == "__main__":
    raise SystemExit(main())
