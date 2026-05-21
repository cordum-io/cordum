from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from typing import cast
from typing import Dict

if TYPE_CHECKING:
    from ..models.role_definition import RoleDefinition


T = TypeVar("T", bound="PutRoleResponse200")


@_attrs_define
class PutRoleResponse200:
    """
    Attributes:
        role (RoleDefinition):
    """

    role: "RoleDefinition"
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.role_definition import RoleDefinition

        role = self.role.to_dict()

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "role": role,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.role_definition import RoleDefinition

        d = src_dict.copy()
        role = RoleDefinition.from_dict(d.pop("role"))

        put_role_response_200 = cls(
            role=role,
        )

        put_role_response_200.additional_properties = d
        return put_role_response_200

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
