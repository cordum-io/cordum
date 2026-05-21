from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import Union


T = TypeVar("T", bound="GetMcpGatewayHealthResponse200")


@_attrs_define
class GetMcpGatewayHealthResponse200:
    """
    Attributes:
        status (Union[Unset, str]):
        gateway_enabled (Union[Unset, bool]):
        component (Union[Unset, str]):
    """

    status: Union[Unset, str] = UNSET
    gateway_enabled: Union[Unset, bool] = UNSET
    component: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        status = self.status

        gateway_enabled = self.gateway_enabled

        component = self.component

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if status is not UNSET:
            field_dict["status"] = status
        if gateway_enabled is not UNSET:
            field_dict["gateway_enabled"] = gateway_enabled
        if component is not UNSET:
            field_dict["component"] = component

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        status = d.pop("status", UNSET)

        gateway_enabled = d.pop("gateway_enabled", UNSET)

        component = d.pop("component", UNSET)

        get_mcp_gateway_health_response_200 = cls(
            status=status,
            gateway_enabled=gateway_enabled,
            component=component,
        )

        get_mcp_gateway_health_response_200.additional_properties = d
        return get_mcp_gateway_health_response_200

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
