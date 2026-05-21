from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast
from typing import cast, List
from typing import Dict
from typing import Union

if TYPE_CHECKING:
    from ..models.pack_verification_checks_item import PackVerificationChecksItem


T = TypeVar("T", bound="PackVerification")


@_attrs_define
class PackVerification:
    """
    Attributes:
        pack_id (Union[Unset, str]):
        valid (Union[Unset, bool]):
        checks (Union[Unset, List['PackVerificationChecksItem']]):
    """

    pack_id: Union[Unset, str] = UNSET
    valid: Union[Unset, bool] = UNSET
    checks: Union[Unset, List["PackVerificationChecksItem"]] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.pack_verification_checks_item import PackVerificationChecksItem

        pack_id = self.pack_id

        valid = self.valid

        checks: Union[Unset, List[Dict[str, Any]]] = UNSET
        if not isinstance(self.checks, Unset):
            checks = []
            for checks_item_data in self.checks:
                checks_item = checks_item_data.to_dict()
                checks.append(checks_item)

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if pack_id is not UNSET:
            field_dict["pack_id"] = pack_id
        if valid is not UNSET:
            field_dict["valid"] = valid
        if checks is not UNSET:
            field_dict["checks"] = checks

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.pack_verification_checks_item import PackVerificationChecksItem

        d = src_dict.copy()
        pack_id = d.pop("pack_id", UNSET)

        valid = d.pop("valid", UNSET)

        checks = []
        _checks = d.pop("checks", UNSET)
        for checks_item_data in _checks or []:
            checks_item = PackVerificationChecksItem.from_dict(checks_item_data)

            checks.append(checks_item)

        pack_verification = cls(
            pack_id=pack_id,
            valid=valid,
            checks=checks,
        )

        pack_verification.additional_properties = d
        return pack_verification

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
