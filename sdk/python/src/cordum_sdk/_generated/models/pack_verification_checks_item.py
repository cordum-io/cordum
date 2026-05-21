from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import Union


T = TypeVar("T", bound="PackVerificationChecksItem")


@_attrs_define
class PackVerificationChecksItem:
    """
    Attributes:
        name (Union[Unset, str]):
        passed (Union[Unset, bool]):
        message (Union[Unset, str]):
    """

    name: Union[Unset, str] = UNSET
    passed: Union[Unset, bool] = UNSET
    message: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        name = self.name

        passed = self.passed

        message = self.message

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if name is not UNSET:
            field_dict["name"] = name
        if passed is not UNSET:
            field_dict["passed"] = passed
        if message is not UNSET:
            field_dict["message"] = message

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        name = d.pop("name", UNSET)

        passed = d.pop("passed", UNSET)

        message = d.pop("message", UNSET)

        pack_verification_checks_item = cls(
            name=name,
            passed=passed,
            message=message,
        )

        pack_verification_checks_item.additional_properties = d
        return pack_verification_checks_item

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
