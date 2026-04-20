from __future__ import annotations

import runpy
from pathlib import Path

import httpx
import respx

EXAMPLES_DIR = Path(__file__).resolve().parents[1] / "examples"


def test_hello_world_example_smoke(monkeypatch, capsys) -> None:
    monkeypatch.setenv("CORDUM_BASE_URL", "http://localhost:8080")
    monkeypatch.setenv("CORDUM_API_KEY", "dev-api-key")
    monkeypatch.setenv("CORDUM_TENANT_ID", "tenant-dev")

    with respx.mock(base_url="http://localhost:8080", assert_all_mocked=True) as router:
        router.get("/api/v1/jobs").mock(
            return_value=httpx.Response(
                200,
                json={
                    "items": [
                        {
                            "id": "job-1",
                            "state": "queued",
                            "topic": "job.default",
                            "tenant": "tenant-dev",
                            "updated_at": "2026-04-20T07:00:00Z",
                        }
                    ],
                    "next_cursor": None,
                },
            )
        )

        runpy.run_path(str(EXAMPLES_DIR / "hello_world.py"), run_name="__main__")

    output = capsys.readouterr().out
    assert "Found 1 jobs" in output
    assert "job-1" in output


def test_async_streaming_example_smoke(monkeypatch, capsys) -> None:
    monkeypatch.setenv("CORDUM_BASE_URL", "http://localhost:8080")
    monkeypatch.setenv("CORDUM_API_KEY", "dev-api-key")
    monkeypatch.setenv("CORDUM_TENANT_ID", "tenant-dev")

    with respx.mock(base_url="http://localhost:8080", assert_all_mocked=True) as router:
        router.get("/api/v1/stream").mock(
            return_value=httpx.Response(
                200,
                headers={"Content-Type": "text/event-stream"},
                content=b"event: hello\ndata: {\"ok\": true}\n\n",
            )
        )

        runpy.run_path(str(EXAMPLES_DIR / "async_streaming.py"), run_name="__main__")

    output = capsys.readouterr().out
    assert "event=hello" in output
    assert "ok" in output
