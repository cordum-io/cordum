from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import Union


T = TypeVar("T", bound="GetMcpGatewayConfigResponse200")


@_attrs_define
class GetMcpGatewayConfigResponse200:
    """
    Attributes:
        gateway_enabled (Union[Unset, bool]):
        upstream_count (Union[Unset, int]):
        upstream_forwarding (Union[Unset, str]):
    """

    gateway_enabled: Union[Unset, bool] = UNSET
    upstream_count: Union[Unset, int] = UNSET
    upstream_forwarding: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        gateway_enabled = self.gateway_enabled

        upstream_count = self.upstream_count

        upstream_forwarding = self.upstream_forwarding

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if gateway_enabled is not UNSET:
            field_dict["gateway_enabled"] = gateway_enabled
        if upstream_count is not UNSET:
            field_dict["upstream_count"] = upstream_count
        if upstream_forwarding is not UNSET:
            field_dict["upstream_forwarding"] = upstream_forwarding

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        gateway_enabled = d.pop("gateway_enabled", UNSET)

        upstream_count = d.pop("upstream_count", UNSET)

        upstream_forwarding = d.pop("upstream_forwarding", UNSET)

        get_mcp_gateway_config_response_200 = cls(
            gateway_enabled=gateway_enabled,
            upstream_count=upstream_count,
            upstream_forwarding=upstream_forwarding,
        )

        get_mcp_gateway_config_response_200.additional_properties = d
        return get_mcp_gateway_config_response_200

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
