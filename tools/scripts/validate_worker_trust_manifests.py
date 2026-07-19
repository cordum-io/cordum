#!/usr/bin/env python
"""Validate fail-closed worker-trust deployment defaults."""

from __future__ import annotations

import sys
from pathlib import Path
from typing import Any

import yaml

HANDSHAKE = "CORDUM_SDK_HANDSHAKE"
HEARTBEAT = "CORDUM_HEARTBEAT_MODE"
COMPOSE_FILES = {
    "docker-compose.yml": ("scheduler", "api-gateway"),
    "docker-compose.release.yml": ("scheduler", "api-gateway"),
    "docker-compose.ha.yaml": ("scheduler-2", "api-gateway-2"),
}


def load_yaml(path: Path) -> Any:
    with path.open(encoding="utf-8") as handle:
        return yaml.safe_load(handle)


def compose_env(service: dict[str, Any]) -> dict[str, str]:
    raw = service.get("environment", {})
    if isinstance(raw, dict):
        return {str(key): str(value) for key, value in raw.items()}
    result: dict[str, str] = {}
    for entry in raw or []:
        key, separator, value = str(entry).partition("=")
        if separator:
            result[key] = value
    return result


def check_pair(label: str, env: dict[str, str], expected: dict[str, str]) -> list[str]:
    errors: list[str] = []
    for name, value in expected.items():
        if env.get(name) != value:
            errors.append(f"{label} must explicitly default {name}={value.rsplit(':-', 1)[-1].rstrip('}')}")
    return errors


def check_compose(root: Path) -> list[str]:
    expected = {HANDSHAKE: "${CORDUM_SDK_HANDSHAKE:-off}", HEARTBEAT: "${CORDUM_HEARTBEAT_MODE:-authority}"}
    errors: list[str] = []
    for relative, services in COMPOSE_FILES.items():
        document = load_yaml(root / relative)
        for name in services:
            service = document.get("services", {}).get(name, {})
            errors.extend(check_pair(f"{relative} {name}", compose_env(service), expected))
    return errors


def deployment_env(document: dict[str, Any]) -> dict[str, str]:
    containers = document["spec"]["template"]["spec"].get("containers", [])
    if not containers:
        return {}
    return {entry["name"]: str(entry.get("value", "")) for entry in containers[0].get("env", [])}


def check_kubernetes(root: Path) -> list[str]:
    targets = {"cordum-scheduler", "cordum-api-gateway"}
    found: set[str] = set()
    errors: list[str] = []
    with (root / "deploy/k8s/base.yaml").open(encoding="utf-8") as handle:
        for document in yaml.safe_load_all(handle):
            if not document or document.get("kind") != "Deployment":
                continue
            name = document.get("metadata", {}).get("name", "")
            if name not in targets:
                continue
            found.add(name)
            errors.extend(check_pair(f"deploy/k8s/base.yaml {name}", deployment_env(document), {HANDSHAKE: "off", HEARTBEAT: "authority"}))
    for missing in sorted(targets - found):
        errors.append(f"deploy/k8s/base.yaml missing deployment {missing}")
    return errors


def nested(data: dict[str, Any], path: str) -> str:
    value: Any = data
    for part in path.split("."):
        value = value.get(part, {}) if isinstance(value, dict) else {}
    return str(value) if value is not None else ""


def check_helm(root: Path) -> list[str]:
    values = load_yaml(root / "cordum-helm/values.yaml")
    mode = nested(values, "workerTrust.mode")
    heartbeat = nested(values, "workerTrust.heartbeatMode")
    errors: list[str] = []
    if mode not in {"off", "warn", "enforce"}:
        errors.append("cordum-helm/values.yaml workerTrust.mode is invalid")
    if heartbeat not in {"authority", "warn", "telemetry"}:
        errors.append("cordum-helm/values.yaml workerTrust.heartbeatMode is invalid")
    if (mode == "off") != (heartbeat == "authority"):
        errors.append("cordum-helm/values.yaml workerTrust mode pair is contradictory")
    if mode == "off":
        return errors
    required = (
        "schedulerId", "schedulerKeyId", "schedulerProof.privateKeySecret.name",
        "schedulerProof.privateKeySecret.key", "schedulerProof.publicKeySecret.name",
        "schedulerProof.publicKeySecret.key", "sessionSigning.keyId",
        "sessionSigning.privateKeySecret.name", "sessionSigning.privateKeySecret.key",
        "sessionSigning.publicKeySecret.name", "sessionSigning.publicKeySecret.key",
    )
    for path in required:
        if not nested(values, f"workerTrust.{path}"):
            errors.append(f"cordum-helm/values.yaml workerTrust.{path} required in active mode")
    return errors


def main() -> int:
    root = Path(sys.argv[1] if len(sys.argv) > 1 else ".").resolve()
    errors = check_compose(root) + check_kubernetes(root) + check_helm(root)
    if errors:
        for error in errors:
            print(f"FAIL:{error}")
        return 1
    print("OK:scheduler and gateway default to off + authority; Helm active refs are complete")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
