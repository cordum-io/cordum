from __future__ import annotations

import importlib
import re
from pathlib import Path

PACKAGE_ROOT = Path(__file__).resolve().parents[1]
SPEC_PATH = PACKAGE_ROOT / ".." / ".." / "docs" / "api" / "openapi" / "cordum-api.yaml"
GENERATED_API_ROOT = PACKAGE_ROOT / "src" / "cordum_sdk" / "_generated" / "api"


def _operation_id_count() -> int:
    spec_text = SPEC_PATH.read_text(encoding="utf-8")
    return len(re.findall(r"^\s*operationId:\s*\S+", spec_text, flags=re.MULTILINE))


def _generated_endpoint_modules() -> list[str]:
    modules: list[str] = []
    for path in sorted(GENERATED_API_ROOT.rglob("*.py")):
        if path.name == "__init__.py":
            continue
        relative = path.relative_to(GENERATED_API_ROOT).with_suffix("")
        modules.append("cordum_sdk._generated.api." + ".".join(relative.parts))
    return modules


def test_every_operation_id_generates_endpoint_module() -> None:
    modules = _generated_endpoint_modules()
    assert len(modules) == _operation_id_count()


def test_every_generated_endpoint_exports_sync_and_async_callables() -> None:
    for module_name in _generated_endpoint_modules():
        module = importlib.import_module(module_name)
        assert callable(getattr(module, "sync_detailed", None)), module_name
        assert callable(getattr(module, "asyncio_detailed", None)), module_name
        sync = getattr(module, "sync", None)
        if sync is not None:
            assert callable(sync), module_name
        asyncio_fn = getattr(module, "asyncio", None)
        if asyncio_fn is not None:
            assert callable(asyncio_fn), module_name
