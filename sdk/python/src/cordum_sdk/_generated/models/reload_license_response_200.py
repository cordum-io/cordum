from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast
from typing import Dict
from typing import Union

if TYPE_CHECKING:
    from ..models.license_info import LicenseInfo


T = TypeVar("T", bound="ReloadLicenseResponse200")


@_attrs_define
class ReloadLicenseResponse200:
    """
    Attributes:
        status (str):
        plan (str):
        license_ (Union[Unset, LicenseInfo]):
    """

    status: str
    plan: str
    license_: Union[Unset, "LicenseInfo"] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.license_info import LicenseInfo

        status = self.status

        plan = self.plan

        license_: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.license_, Unset):
            license_ = self.license_.to_dict()

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
                "plan": plan,
            }
        )
        if license_ is not UNSET:
            field_dict["license"] = license_

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.license_info import LicenseInfo

        d = src_dict.copy()
        status = d.pop("status")

        plan = d.pop("plan")

        _license_ = d.pop("license", UNSET)
        license_: Union[Unset, LicenseInfo]
        if isinstance(_license_, Unset):
            license_ = UNSET
        else:
            license_ = LicenseInfo.from_dict(_license_)

        reload_license_response_200 = cls(
            status=status,
            plan=plan,
            license_=license_,
        )

        reload_license_response_200.additional_properties = d
        return reload_license_response_200

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
