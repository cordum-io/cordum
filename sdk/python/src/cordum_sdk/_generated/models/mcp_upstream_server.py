from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.mcp_upstream_server_risk import MCPUpstreamServerRisk
from ..models.mcp_upstream_server_transport import MCPUpstreamServerTransport
from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import cast, List
from typing import Dict
from typing import Union
import datetime

if TYPE_CHECKING:
    from ..models.mcp_upstream_server_labels import MCPUpstreamServerLabels


T = TypeVar("T", bound="MCPUpstreamServer")


@_attrs_define
class MCPUpstreamServer:
    """
    Attributes:
        name (str):
        transport (MCPUpstreamServerTransport):
        tenant_id (str): Tenant scope for the upstream; `*` is reserved for system-wide entries.
        risk (MCPUpstreamServerRisk):
        enabled (bool):
        endpoint (Union[Unset, str]): HTTPS endpoint for http/sse transports; unsafe local endpoints are rejected in
            strict policy modes.
        command (Union[Unset, List[str]]): Shell-free argv vector for stdio transport. Shell metacharacters are
            rejected.
        auth_secret_ref (Union[Unset, str]): Reference to auth material. Raw secrets are rejected and never resolved in
            registry responses.
        labels (Union[Unset, MCPUpstreamServerLabels]):
        created_at (Union[Unset, datetime.datetime]):
        updated_at (Union[Unset, datetime.datetime]):
    """

    name: str
    transport: MCPUpstreamServerTransport
    tenant_id: str
    risk: MCPUpstreamServerRisk
    enabled: bool
    endpoint: Union[Unset, str] = UNSET
    command: Union[Unset, List[str]] = UNSET
    auth_secret_ref: Union[Unset, str] = UNSET
    labels: Union[Unset, "MCPUpstreamServerLabels"] = UNSET
    created_at: Union[Unset, datetime.datetime] = UNSET
    updated_at: Union[Unset, datetime.datetime] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.mcp_upstream_server_labels import MCPUpstreamServerLabels

        name = self.name

        transport = self.transport.value

        tenant_id = self.tenant_id

        risk = self.risk.value

        enabled = self.enabled

        endpoint = self.endpoint

        command: Union[Unset, List[str]] = UNSET
        if not isinstance(self.command, Unset):
            command = self.command

        auth_secret_ref = self.auth_secret_ref

        labels: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.labels, Unset):
            labels = self.labels.to_dict()

        created_at: Union[Unset, str] = UNSET
        if not isinstance(self.created_at, Unset):
            created_at = self.created_at.isoformat()

        updated_at: Union[Unset, str] = UNSET
        if not isinstance(self.updated_at, Unset):
            updated_at = self.updated_at.isoformat()

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "transport": transport,
                "tenant_id": tenant_id,
                "risk": risk,
                "enabled": enabled,
            }
        )
        if endpoint is not UNSET:
            field_dict["endpoint"] = endpoint
        if command is not UNSET:
            field_dict["command"] = command
        if auth_secret_ref is not UNSET:
            field_dict["auth_secret_ref"] = auth_secret_ref
        if labels is not UNSET:
            field_dict["labels"] = labels
        if created_at is not UNSET:
            field_dict["created_at"] = created_at
        if updated_at is not UNSET:
            field_dict["updated_at"] = updated_at

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.mcp_upstream_server_labels import MCPUpstreamServerLabels

        d = src_dict.copy()
        name = d.pop("name")

        transport = MCPUpstreamServerTransport(d.pop("transport"))

        tenant_id = d.pop("tenant_id")

        risk = MCPUpstreamServerRisk(d.pop("risk"))

        enabled = d.pop("enabled")

        endpoint = d.pop("endpoint", UNSET)

        command = cast(List[str], d.pop("command", UNSET))

        auth_secret_ref = d.pop("auth_secret_ref", UNSET)

        _labels = d.pop("labels", UNSET)
        labels: Union[Unset, MCPUpstreamServerLabels]
        if isinstance(_labels, Unset):
            labels = UNSET
        else:
            labels = MCPUpstreamServerLabels.from_dict(_labels)

        _created_at = d.pop("created_at", UNSET)
        created_at: Union[Unset, datetime.datetime]
        if isinstance(_created_at, Unset):
            created_at = UNSET
        else:
            created_at = isoparse(_created_at)

        _updated_at = d.pop("updated_at", UNSET)
        updated_at: Union[Unset, datetime.datetime]
        if isinstance(_updated_at, Unset):
            updated_at = UNSET
        else:
            updated_at = isoparse(_updated_at)

        mcp_upstream_server = cls(
            name=name,
            transport=transport,
            tenant_id=tenant_id,
            risk=risk,
            enabled=enabled,
            endpoint=endpoint,
            command=command,
            auth_secret_ref=auth_secret_ref,
            labels=labels,
            created_at=created_at,
            updated_at=updated_at,
        )

        mcp_upstream_server.additional_properties = d
        return mcp_upstream_server

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
