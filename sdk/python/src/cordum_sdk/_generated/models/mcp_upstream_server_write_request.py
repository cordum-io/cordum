from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.mcp_upstream_server_write_request_risk import MCPUpstreamServerWriteRequestRisk
from ..models.mcp_upstream_server_write_request_transport import (
    MCPUpstreamServerWriteRequestTransport,
)
from ..types import UNSET, Unset
from typing import cast
from typing import cast, List
from typing import Dict
from typing import Union

if TYPE_CHECKING:
    from ..models.mcp_upstream_server_write_request_labels import (
        MCPUpstreamServerWriteRequestLabels,
    )


T = TypeVar("T", bound="MCPUpstreamServerWriteRequest")


@_attrs_define
class MCPUpstreamServerWriteRequest:
    """Write payload for an upstream MCP entry. `tenant_id` may be omitted and is resolved from X-Tenant-ID; `created_at`
    and `updated_at` are server-managed.

        Attributes:
            name (Union[Unset, str]):
            transport (Union[Unset, MCPUpstreamServerWriteRequestTransport]):
            endpoint (Union[Unset, str]):
            command (Union[Unset, List[str]]):
            tenant_id (Union[Unset, str]):
            auth_secret_ref (Union[Unset, str]):
            labels (Union[Unset, MCPUpstreamServerWriteRequestLabels]):
            risk (Union[Unset, MCPUpstreamServerWriteRequestRisk]):
            enabled (Union[Unset, bool]):
    """

    name: Union[Unset, str] = UNSET
    transport: Union[Unset, MCPUpstreamServerWriteRequestTransport] = UNSET
    endpoint: Union[Unset, str] = UNSET
    command: Union[Unset, List[str]] = UNSET
    tenant_id: Union[Unset, str] = UNSET
    auth_secret_ref: Union[Unset, str] = UNSET
    labels: Union[Unset, "MCPUpstreamServerWriteRequestLabels"] = UNSET
    risk: Union[Unset, MCPUpstreamServerWriteRequestRisk] = UNSET
    enabled: Union[Unset, bool] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.mcp_upstream_server_write_request_labels import (
            MCPUpstreamServerWriteRequestLabels,
        )

        name = self.name

        transport: Union[Unset, str] = UNSET
        if not isinstance(self.transport, Unset):
            transport = self.transport.value

        endpoint = self.endpoint

        command: Union[Unset, List[str]] = UNSET
        if not isinstance(self.command, Unset):
            command = self.command

        tenant_id = self.tenant_id

        auth_secret_ref = self.auth_secret_ref

        labels: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.labels, Unset):
            labels = self.labels.to_dict()

        risk: Union[Unset, str] = UNSET
        if not isinstance(self.risk, Unset):
            risk = self.risk.value

        enabled = self.enabled

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if name is not UNSET:
            field_dict["name"] = name
        if transport is not UNSET:
            field_dict["transport"] = transport
        if endpoint is not UNSET:
            field_dict["endpoint"] = endpoint
        if command is not UNSET:
            field_dict["command"] = command
        if tenant_id is not UNSET:
            field_dict["tenant_id"] = tenant_id
        if auth_secret_ref is not UNSET:
            field_dict["auth_secret_ref"] = auth_secret_ref
        if labels is not UNSET:
            field_dict["labels"] = labels
        if risk is not UNSET:
            field_dict["risk"] = risk
        if enabled is not UNSET:
            field_dict["enabled"] = enabled

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.mcp_upstream_server_write_request_labels import (
            MCPUpstreamServerWriteRequestLabels,
        )

        d = src_dict.copy()
        name = d.pop("name", UNSET)

        _transport = d.pop("transport", UNSET)
        transport: Union[Unset, MCPUpstreamServerWriteRequestTransport]
        if isinstance(_transport, Unset):
            transport = UNSET
        else:
            transport = MCPUpstreamServerWriteRequestTransport(_transport)

        endpoint = d.pop("endpoint", UNSET)

        command = cast(List[str], d.pop("command", UNSET))

        tenant_id = d.pop("tenant_id", UNSET)

        auth_secret_ref = d.pop("auth_secret_ref", UNSET)

        _labels = d.pop("labels", UNSET)
        labels: Union[Unset, MCPUpstreamServerWriteRequestLabels]
        if isinstance(_labels, Unset):
            labels = UNSET
        else:
            labels = MCPUpstreamServerWriteRequestLabels.from_dict(_labels)

        _risk = d.pop("risk", UNSET)
        risk: Union[Unset, MCPUpstreamServerWriteRequestRisk]
        if isinstance(_risk, Unset):
            risk = UNSET
        else:
            risk = MCPUpstreamServerWriteRequestRisk(_risk)

        enabled = d.pop("enabled", UNSET)

        mcp_upstream_server_write_request = cls(
            name=name,
            transport=transport,
            endpoint=endpoint,
            command=command,
            tenant_id=tenant_id,
            auth_secret_ref=auth_secret_ref,
            labels=labels,
            risk=risk,
            enabled=enabled,
        )

        mcp_upstream_server_write_request.additional_properties = d
        return mcp_upstream_server_write_request

    @property
    def additional_keys(self) -> List[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
