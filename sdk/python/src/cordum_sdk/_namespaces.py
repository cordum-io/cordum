from __future__ import annotations

import importlib
import pkgutil
from dataclasses import dataclass
from functools import lru_cache
from types import ModuleType
from typing import Any, Callable, Dict, Mapping, Optional, Tuple


@dataclass(frozen=True)
class NamespaceDefinition:
    packages: Tuple[str, ...]
    include: Optional[Tuple[str, ...]] = None

    def matches(self, operation_name: str) -> bool:
        if self.include is None:
            return True
        return operation_name in self.include


_NAMESPACE_DEFINITIONS: Dict[str, NamespaceDefinition] = {
    "jobs": NamespaceDefinition(("jobs",)),
    "workflows": NamespaceDefinition(("workflows", "workflow_runs")),
    "policies": NamespaceDefinition(("policy",)),
    "workers": NamespaceDefinition(("workers",)),
    "agents": NamespaceDefinition(("workers",)),
    "mcp": NamespaceDefinition(("mcp",)),
    "telemetry": NamespaceDefinition(("telemetry", "audit_export")),
    "auth": NamespaceDefinition(("auth", "api_keys")),
    "identities": NamespaceDefinition(("users",)),
    "credentials": NamespaceDefinition(("worker_credentials",)),
    "velocity": NamespaceDefinition(
        ("policy",),
        include=(
            "create_velocity_rule",
            "delete_velocity_rule",
            "get_velocity_rule",
            "list_velocity_rules",
            "test_velocity_rule",
            "update_velocity_rule",
        ),
    ),
    "legal_hold": NamespaceDefinition(("legal_hold",)),
    "rbac": NamespaceDefinition(
        ("auth", "users"),
        include=(
            "create_user",
            "delete_role",
            "delete_user",
            "get_role",
            "list_roles",
            "list_users",
            "put_role",
            "reset_user_password",
            "update_user",
        ),
    ),
}


@lru_cache(maxsize=None)
def get_namespace_operations(namespace_name: str) -> Dict[str, ModuleType]:
    definition = _NAMESPACE_DEFINITIONS.get(namespace_name)
    if definition is None:
        raise KeyError(namespace_name)

    operations: Dict[str, ModuleType] = {}
    for package_name in definition.packages:
        for operation_name, module in _load_package_operations(package_name).items():
            if definition.matches(operation_name):
                operations[operation_name] = module
    return operations


@lru_cache(maxsize=None)
def get_available_namespaces() -> Tuple[str, ...]:
    return tuple(sorted(_NAMESPACE_DEFINITIONS))


@lru_cache(maxsize=None)
def _load_package_operations(package_name: str) -> Dict[str, ModuleType]:
    package = importlib.import_module("cordum_sdk._generated.api.{name}".format(name=package_name))

    operations: Dict[str, ModuleType] = {}
    for module_info in pkgutil.iter_modules(package.__path__):
        if module_info.name.startswith("_"):
            continue
        operation_name = module_info.name
        operations[operation_name] = importlib.import_module(
            "{package}.{module}".format(package=package.__name__, module=operation_name)
        )
    return operations


class ResourceNamespace:
    def __init__(
        self,
        *,
        owner: Any,
        namespace_name: str,
        operations: Mapping[str, ModuleType],
        async_mode: bool,
    ) -> None:
        self._owner = owner
        self._namespace_name = namespace_name
        self._operations = dict(operations)
        self._async_mode = async_mode
        self._callables: Dict[str, Callable[..., Any]] = {}

    def __dir__(self) -> list[str]:
        return sorted(self._operations)

    def __getattr__(self, operation_name: str) -> Callable[..., Any]:
        module = self._operations.get(operation_name)
        if module is None:
            raise AttributeError(
                "{namespace} has no operation {operation!r}".format(
                    namespace=self._namespace_name,
                    operation=operation_name,
                )
            )

        cached = self._callables.get(operation_name)
        if cached is not None:
            return cached

        if self._async_mode:

            async def async_call(*args: Any, **kwargs: Any) -> Any:
                return await self._owner._invoke_operation(module, *args, **kwargs)

            call = async_call

        else:

            def sync_call(*args: Any, **kwargs: Any) -> Any:
                return self._owner._invoke_operation(module, *args, **kwargs)

            call = sync_call

        call.__name__ = operation_name
        call.__qualname__ = "{namespace}.{operation}".format(
            namespace=self._namespace_name,
            operation=operation_name,
        )
        call.__doc__ = getattr(module, "sync_detailed", None).__doc__
        self._callables[operation_name] = call
        return call

    def __repr__(self) -> str:
        return "ResourceNamespace(name={!r}, operations={!r})".format(
            self._namespace_name,
            sorted(self._operations),
        )

    def paginate(self, operation_name: str, *args: Any, **kwargs: Any) -> Any:
        module = self._operations.get(operation_name)
        if module is None:
            raise AttributeError(
                "{namespace} has no operation {operation!r}".format(
                    namespace=self._namespace_name,
                    operation=operation_name,
                )
            )

        if self._async_mode:
            from .pagination import paginate_async

            async def async_operation_with_headers(**page_kwargs: Any) -> Any:
                return await self._owner._invoke_paginated(module, *args, **page_kwargs)

            return paginate_async(async_operation_with_headers, **kwargs)

        from .pagination import paginate

        def sync_operation_with_headers(**page_kwargs: Any) -> Any:
            return self._owner._invoke_paginated(module, *args, **page_kwargs)

        return paginate(sync_operation_with_headers, **kwargs)

    def stream(self, *args: Any, **kwargs: Any) -> Any:
        return self._owner._stream(*args, **kwargs)
