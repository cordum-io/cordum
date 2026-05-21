from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from typing import cast
from typing import cast, List
from typing import Dict

if TYPE_CHECKING:
    from ..models.role_definition import RoleDefinition


T = TypeVar("T", bound="RoleListResponse")


@_attrs_define
class RoleListResponse:
    """
    Attributes:
        roles (List['RoleDefinition']):
        entitled (bool):
    """

    roles: List["RoleDefinition"]
    entitled: bool
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.role_definition import RoleDefinition

        roles = []
        for roles_item_data in self.roles:
            roles_item = roles_item_data.to_dict()
            roles.append(roles_item)

        entitled = self.entitled

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "roles": roles,
                "entitled": entitled,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.role_definition import RoleDefinition

        d = src_dict.copy()
        roles = []
        _roles = d.pop("roles")
        for roles_item_data in _roles:
            roles_item = RoleDefinition.from_dict(roles_item_data)

            roles.append(roles_item)

        entitled = d.pop("entitled")

        role_list_response = cls(
            roles=roles,
            entitled=entitled,
        )

        role_list_response.additional_properties = d
        return role_list_response

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
