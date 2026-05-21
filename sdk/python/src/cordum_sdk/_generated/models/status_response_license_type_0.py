from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import Union


T = TypeVar("T", bound="StatusResponseLicenseType0")


@_attrs_define
class StatusResponseLicenseType0:
    """
    Attributes:
        valid (Union[Unset, bool]):
        tier (Union[Unset, str]):
    """

    valid: Union[Unset, bool] = UNSET
    tier: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        valid = self.valid

        tier = self.tier

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if valid is not UNSET:
            field_dict["valid"] = valid
        if tier is not UNSET:
            field_dict["tier"] = tier

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        valid = d.pop("valid", UNSET)

        tier = d.pop("tier", UNSET)

        status_response_license_type_0 = cls(
            valid=valid,
            tier=tier,
        )

        status_response_license_type_0.additional_properties = d
        return status_response_license_type_0

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
