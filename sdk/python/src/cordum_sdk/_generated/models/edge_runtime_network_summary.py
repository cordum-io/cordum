from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.edge_runtime_network_summary_protocol import EdgeRuntimeNetworkSummaryProtocol
from ..types import UNSET, Unset
from typing import Union


T = TypeVar("T", bound="EdgeRuntimeNetworkSummary")


@_attrs_define
class EdgeRuntimeNetworkSummary:
    """
    Attributes:
        host_redacted (Union[Unset, str]):
        ip_prefix (Union[Unset, str]):
        port (Union[Unset, int]):
        protocol (Union[Unset, EdgeRuntimeNetworkSummaryProtocol]):
    """

    host_redacted: Union[Unset, str] = UNSET
    ip_prefix: Union[Unset, str] = UNSET
    port: Union[Unset, int] = UNSET
    protocol: Union[Unset, EdgeRuntimeNetworkSummaryProtocol] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        host_redacted = self.host_redacted

        ip_prefix = self.ip_prefix

        port = self.port

        protocol: Union[Unset, str] = UNSET
        if not isinstance(self.protocol, Unset):
            protocol = self.protocol.value

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if host_redacted is not UNSET:
            field_dict["host_redacted"] = host_redacted
        if ip_prefix is not UNSET:
            field_dict["ip_prefix"] = ip_prefix
        if port is not UNSET:
            field_dict["port"] = port
        if protocol is not UNSET:
            field_dict["protocol"] = protocol

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        host_redacted = d.pop("host_redacted", UNSET)

        ip_prefix = d.pop("ip_prefix", UNSET)

        port = d.pop("port", UNSET)

        _protocol = d.pop("protocol", UNSET)
        protocol: Union[Unset, EdgeRuntimeNetworkSummaryProtocol]
        if isinstance(_protocol, Unset):
            protocol = UNSET
        else:
            protocol = EdgeRuntimeNetworkSummaryProtocol(_protocol)

        edge_runtime_network_summary = cls(
            host_redacted=host_redacted,
            ip_prefix=ip_prefix,
            port=port,
            protocol=protocol,
        )

        edge_runtime_network_summary.additional_properties = d
        return edge_runtime_network_summary

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
