from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import cast, List
from typing import Union
import datetime


T = TypeVar("T", bound="RoleDefinition")


@_attrs_define
class RoleDefinition:
    """
    Attributes:
        name (str):
        description (Union[Unset, str]):
        permissions (Union[Unset, List[str]]):
        inherits (Union[Unset, List[str]]):
        built_in (Union[Unset, bool]):
        created_at (Union[Unset, datetime.datetime]):
        updated_at (Union[Unset, datetime.datetime]):
    """

    name: str
    description: Union[Unset, str] = UNSET
    permissions: Union[Unset, List[str]] = UNSET
    inherits: Union[Unset, List[str]] = UNSET
    built_in: Union[Unset, bool] = UNSET
    created_at: Union[Unset, datetime.datetime] = UNSET
    updated_at: Union[Unset, datetime.datetime] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        name = self.name

        description = self.description

        permissions: Union[Unset, List[str]] = UNSET
        if not isinstance(self.permissions, Unset):
            permissions = self.permissions

        inherits: Union[Unset, List[str]] = UNSET
        if not isinstance(self.inherits, Unset):
            inherits = self.inherits

        built_in = self.built_in

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
            }
        )
        if description is not UNSET:
            field_dict["description"] = description
        if permissions is not UNSET:
            field_dict["permissions"] = permissions
        if inherits is not UNSET:
            field_dict["inherits"] = inherits
        if built_in is not UNSET:
            field_dict["built_in"] = built_in
        if created_at is not UNSET:
            field_dict["created_at"] = created_at
        if updated_at is not UNSET:
            field_dict["updated_at"] = updated_at

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        name = d.pop("name")

        description = d.pop("description", UNSET)

        permissions = cast(List[str], d.pop("permissions", UNSET))

        inherits = cast(List[str], d.pop("inherits", UNSET))

        built_in = d.pop("built_in", UNSET)

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

        role_definition = cls(
            name=name,
            description=description,
            permissions=permissions,
            inherits=inherits,
            built_in=built_in,
            created_at=created_at,
            updated_at=updated_at,
        )

        role_definition.additional_properties = d
        return role_definition

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
