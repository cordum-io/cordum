"""SDK-backed driver for the Python conformance harness.

Every request goes through the shipped ``cordum_sdk`` client so the suite
grades the real auth/retry/error behavior, not a parallel ``urllib`` stack.
"""

from __future__ import annotations

import time
from dataclasses import dataclass, field
from typing import Any, Optional

from cordum_sdk.client import CordumClient, RequestResponse
from cordum_sdk.auth import ApiKeyAuth, BearerTokenAuth
from cordum_sdk.errors import CordumError
from cordum_sdk._retry import RetryExhaustedError, RetryPolicy

from . import _diff
from ._operation_map import OPERATION_MAP


@dataclass
class Fixture:
    schema_version: int
    name: str
    description: str = ""
    tags: list[str] = field(default_factory=list)
    setup: dict[str, Any] = field(default_factory=dict)
    steps: list[dict[str, Any]] = field(default_factory=list)

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> "Fixture":
        return cls(
            schema_version=d.get("schemaVersion", 1),
            name=d.get("name", ""),
            description=d.get("description", ""),
            tags=d.get("tags", []) or [],
            setup=d.get("setup", {}) or {},
            steps=d.get("steps", []) or [],
        )


@dataclass
class Driver:
    base_url: str
    api_key: str
    tenant: str
    timeout_sec: float = 10.0
    vars: dict[str, Any] = field(default_factory=dict)

    def reset_vars(self) -> None:
        self.vars = {"apiKey": self.api_key, "tenant": self.tenant}

    def run_fixture(self, fx: Fixture) -> None:
        self.reset_vars()
        for idx, step in enumerate(fx.steps):
            kind = step.get("kind", "request")
            try:
                if kind == "sleep":
                    time.sleep(step.get("durationMs", 0) / 1000.0)
                    continue
                if kind in ("request", "assert_error", "stream", "paginate"):
                    self._dispatch(fx, step)
                else:
                    raise RuntimeError(f"unknown step kind {kind!r}")
            except Exception as exc:
                op = step.get("operationId", "")
                raise RuntimeError(f"step {idx} ({kind} {op}): {exc}") from exc

    def _dispatch(self, fx: Fixture, step: dict[str, Any]) -> None:
        op = step.get("operationId", "")
        if op not in OPERATION_MAP:
            raise RuntimeError(f"unknown operationId {op!r}")
        method, path = OPERATION_MAP[op]

        for key, value in (step.get("pathParams") or {}).items():
            path = path.replace("{" + key + "}", self._resolve_str(value))

        query = step.get("query") or {}
        headers = self._build_headers(fx, step)
        body = _diff.resolve_vars(step.get("body"), self.vars) if step.get("body") is not None else None

        with self._build_client(fx.setup.get("auth"), step.get("auth")) as client:
            if step.get("kind") == "stream":
                self._assert_stream(client, path, headers, query, step)
                return
            if step.get("kind") == "paginate":
                self._assert_paginate(client, method, path, headers, query, step)
                return

            try:
                response = client.request_detailed(
                    method,
                    path,
                    query=query,
                    headers=headers,
                    json_body=body,
                )
            except Exception as exc:  # noqa: BLE001
                if step.get("kind") != "assert_error":
                    raise
                self._assert_error(exc, step)
                return

        if step.get("kind") == "assert_error":
            expected = step.get("expect", {}).get("errorClass") or step.get("errorClass")
            raise RuntimeError(f"expected {expected or 'error'} but request succeeded")

        expect = step.get("expect") or {}
        expected_status = expect.get("status", 0)
        if expected_status and response.status_code != expected_status:
            raise RuntimeError(
                f"status={response.status_code} want {expected_status}; body={response.text[:240]}"
            )

        self._assert_response(response, step)

    def _assert_response(self, response: RequestResponse, step: dict[str, Any]) -> None:
        expect = step.get("expect") or {}
        parsed = response.data

        if expect.get("body") is not None:
            _diff.diff(parsed, expect["body"], "$")
        for path_expr, expected in (expect.get("bodyMatches") or {}).items():
            selected = _diff.select_json_path(parsed, path_expr)
            _diff.diff(selected, expected, path_expr)
        for key, selector in (step.get("extract") or {}).items():
            self.vars[key] = _diff.select_json_path(parsed, selector)

    def _assert_error(self, exc: Exception, step: dict[str, Any]) -> None:
        expect = step.get("expect") or {}
        expected_class = expect.get("errorClass") or step.get("errorClass") or ""
        actual_class = exc.__class__.__name__
        if isinstance(exc, RetryExhaustedError):
            actual_class = "RetryExhaustedError"
        elif isinstance(exc, CordumError):
            actual_class = exc.__class__.__name__

        if expected_class and actual_class != expected_class:
            raise RuntimeError(f"errorClass={actual_class} want {expected_class}")

        expected_status = expect.get("status") or _diff.infer_error_status(expected_class)
        actual_status = getattr(exc, "status_code", None)
        if actual_status is None and isinstance(exc, RetryExhaustedError):
            actual_status = exc.status_code
        if expected_status and actual_status != expected_status:
            raise RuntimeError(f"status={actual_status} want {expected_status}")

        payload = getattr(exc, "payload", None)
        if payload is None and isinstance(exc, RetryExhaustedError) and exc.payload is not None:
            payload = exc.payload
        for selector, expected in (expect.get("fields") or {}).items():
            path_expr = selector if selector.startswith("$") else f"$.{selector}"
            selected = _diff.select_json_path(payload, path_expr)
            _diff.diff(selected, expected, path_expr)

    def _assert_paginate(
        self,
        client: CordumClient,
        method: str,
        path: str,
        headers: dict[str, str],
        query: dict[str, Any],
        step: dict[str, Any],
    ) -> None:
        max_pages = step.get("maxPages") or 10
        page_count = 0
        total_items = 0
        current_query = dict(query)

        while page_count < max_pages:
            response = client.request_detailed(
                method,
                path,
                query=current_query,
                headers=headers,
            )
            page_count += 1

            body = response.data
            if not isinstance(body, dict):
                raise RuntimeError(f"paginate response is {type(body).__name__}, want object")
            items = body.get("items")
            if not isinstance(items, list):
                raise RuntimeError("paginate response missing items array")
            total_items += len(items)

            cursor = body.get("nextCursor") or body.get("next_cursor") or body.get("cursor")
            if not cursor:
                break
            current_query = {**query, "cursor": cursor}

        expect = step.get("expect") or {}
        self._assert_count("pageCount", page_count, expect.get("pageCount"))
        self._assert_count("totalItems", total_items, expect.get("totalItems"))

    def _assert_stream(
        self,
        client: CordumClient,
        path: str,
        headers: dict[str, str],
        query: dict[str, Any],
        step: dict[str, Any],
    ) -> None:
        events = []
        max_events = step.get("maxEvents") or len((step.get("expect") or {}).get("events") or [])
        for event in client.stream(path=path, headers=headers, query=query):
            events.append(event)
            if max_events and len(events) >= max_events:
                break

        if not events:
            raise RuntimeError("stream body carries no SSE frames")

        expected_events = (step.get("expect") or {}).get("events") or []
        if len(events) < len(expected_events):
            raise RuntimeError(f"stream events={len(events)} want >={len(expected_events)}")

        for index, expected in enumerate(expected_events):
            actual = events[index]
            if actual.event != expected.get("type"):
                raise RuntimeError(
                    f"stream event {index} type={actual.event} want {expected.get('type')}"
                )
            _diff.diff(actual.data, expected.get("data"), f"$.events[{index}].data")

    def _build_client(
        self,
        setup_auth: Optional[dict[str, Any]],
        step_auth: Optional[dict[str, Any]],
    ) -> CordumClient:
        auth_config = step_auth if step_auth is not None else setup_auth
        auth: Any
        if auth_config is None:
            auth = self.api_key
        else:
            kind = auth_config.get("kind")
            value = self._resolve_str(auth_config.get("value", ""))
            if kind == "apiKey":
                auth = ApiKeyAuth(value)
            elif kind == "bearer":
                auth = BearerTokenAuth(value)
            elif kind == "none":
                auth = None
            else:
                auth = self.api_key

        return CordumClient(
            base_url=self.base_url,
            auth=auth,
            timeout=self.timeout_sec,
            tenant_id=self.tenant,
            retry_policy=RetryPolicy(max_retries=2),
        )

    def _build_headers(
        self, fx: Fixture, step: dict[str, Any]
    ) -> dict[str, str]:
        headers: dict[str, str] = {}
        for key, value in (fx.setup.get("headers") or {}).items():
            headers[key] = self._resolve_str(value)
        for key, value in (step.get("headers") or {}).items():
            headers[key] = self._resolve_str(value)
        return headers

    def _resolve_str(self, s: Any) -> str:
        if not isinstance(s, str):
            return "" if s is None else str(s)
        if not s.startswith("$vars."):
            return s
        key = s[len("$vars.") :]
        val = self.vars.get(key, "")
        return str(val)

    def _assert_count(self, name: str, actual: int, expected: Any) -> None:
        if expected is None:
            return
        if isinstance(expected, (int, float)):
            if actual != int(expected):
                raise RuntimeError(f"{name}={actual} want {int(expected)}")
            return
        if isinstance(expected, str) and expected.startswith(">="):
            want = int(expected[2:].strip())
            if actual < want:
                raise RuntimeError(f"{name}={actual} want >={want}")
            return
        want = int(expected)
        if actual != want:
            raise RuntimeError(f"{name}={actual} want {want}")
