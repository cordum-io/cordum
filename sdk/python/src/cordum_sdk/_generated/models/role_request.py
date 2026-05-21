from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast, List
from typing import Union


T = TypeVar("T", bound="RoleRequest")


@_attrs_define
class RoleRequest:
    """
    Attributes:
        description (Union[Unset, str]):
        permissions (Union[Unset, List[str]]):
        inherits (Union[Unset, List[str]]):
    """

    description: Union[Unset, str] = UNSET
    permissions: Union[Unset, List[str]] = UNSET
    inherits: Union[Unset, List[str]] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        description = self.description

        permissions: Union[Unset, List[str]] = UNSET
        if not isinstance(self.permissions, Unset):
            permissions = self.permissions

        inherits: Union[Unset, List[str]] = UNSET
        if not isinstance(self.inherits, Unset):
            inherits = self.inherits

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if description is not UNSET:
            field_dict["description"] = description
        if permissions is not UNSET:
            field_dict["permissions"] = permissions
        if inherits is not UNSET:
            field_dict["inherits"] = inherits

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        description = d.pop("description", UNSET)

        permissions = cast(List[str], d.pop("permissions", UNSET))

        inherits = cast(List[str], d.pop("inherits", UNSET))

        role_request = cls(
            description=description,
            permissions=permissions,
            inherits=inherits,
        )

        role_request.additional_properties = d
        return role_request

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
