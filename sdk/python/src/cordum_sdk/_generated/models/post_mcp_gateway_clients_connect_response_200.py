from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import Union


T = TypeVar("T", bound="PostMcpGatewayClientsConnectResponse200")


@_attrs_define
class PostMcpGatewayClientsConnectResponse200:
    """
    Attributes:
        session_id (Union[Unset, str]):
        execution_id (Union[Unset, str]):
        tenant_id (Union[Unset, str]):
    """

    session_id: Union[Unset, str] = UNSET
    execution_id: Union[Unset, str] = UNSET
    tenant_id: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        session_id = self.session_id

        execution_id = self.execution_id

        tenant_id = self.tenant_id

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if session_id is not UNSET:
            field_dict["session_id"] = session_id
        if execution_id is not UNSET:
            field_dict["execution_id"] = execution_id
        if tenant_id is not UNSET:
            field_dict["tenant_id"] = tenant_id

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        session_id = d.pop("session_id", UNSET)

        execution_id = d.pop("execution_id", UNSET)

        tenant_id = d.pop("tenant_id", UNSET)

        post_mcp_gateway_clients_connect_response_200 = cls(
            session_id=session_id,
            execution_id=execution_id,
            tenant_id=tenant_id,
        )

        post_mcp_gateway_clients_connect_response_200.additional_properties = d
        return post_mcp_gateway_clients_connect_response_200

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
