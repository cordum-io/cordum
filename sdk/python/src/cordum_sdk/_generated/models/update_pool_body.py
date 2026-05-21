from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast, List
from typing import Union


T = TypeVar("T", bound="UpdatePoolBody")


@_attrs_define
class UpdatePoolBody:
    """
    Attributes:
        requires (Union[Unset, List[str]]):
        description (Union[Unset, str]):
        status (Union[Unset, str]):
    """

    requires: Union[Unset, List[str]] = UNSET
    description: Union[Unset, str] = UNSET
    status: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        requires: Union[Unset, List[str]] = UNSET
        if not isinstance(self.requires, Unset):
            requires = self.requires

        description = self.description

        status = self.status

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if requires is not UNSET:
            field_dict["requires"] = requires
        if description is not UNSET:
            field_dict["description"] = description
        if status is not UNSET:
            field_dict["status"] = status

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        requires = cast(List[str], d.pop("requires", UNSET))

        description = d.pop("description", UNSET)

        status = d.pop("status", UNSET)

        update_pool_body = cls(
            requires=requires,
            description=description,
            status=status,
        )

        update_pool_body.additional_properties = d
        return update_pool_body

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
