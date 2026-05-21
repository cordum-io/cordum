from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.mcp_status_transport import McpStatusTransport
from ..types import UNSET, Unset
from typing import Union


T = TypeVar("T", bound="McpStatus")


@_attrs_define
class McpStatus:
    """
    Attributes:
        running (Union[Unset, bool]):
        connected_clients (Union[Unset, int]):
        uptime_seconds (Union[Unset, float]):
        transport (Union[Unset, McpStatusTransport]):
    """

    running: Union[Unset, bool] = UNSET
    connected_clients: Union[Unset, int] = UNSET
    uptime_seconds: Union[Unset, float] = UNSET
    transport: Union[Unset, McpStatusTransport] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        running = self.running

        connected_clients = self.connected_clients

        uptime_seconds = self.uptime_seconds

        transport: Union[Unset, str] = UNSET
        if not isinstance(self.transport, Unset):
            transport = self.transport.value

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if running is not UNSET:
            field_dict["running"] = running
        if connected_clients is not UNSET:
            field_dict["connected_clients"] = connected_clients
        if uptime_seconds is not UNSET:
            field_dict["uptime_seconds"] = uptime_seconds
        if transport is not UNSET:
            field_dict["transport"] = transport

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        running = d.pop("running", UNSET)

        connected_clients = d.pop("connected_clients", UNSET)

        uptime_seconds = d.pop("uptime_seconds", UNSET)

        _transport = d.pop("transport", UNSET)
        transport: Union[Unset, McpStatusTransport]
        if isinstance(_transport, Unset):
            transport = UNSET
        else:
            transport = McpStatusTransport(_transport)

        mcp_status = cls(
            running=running,
            connected_clients=connected_clients,
            uptime_seconds=uptime_seconds,
            transport=transport,
        )

        mcp_status.additional_properties = d
        return mcp_status

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
