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
    from ..models.generic_object import GenericObject


T = TypeVar("T", bound="GetLicenseResponse200")


@_attrs_define
class GetLicenseResponse200:
    """
    Attributes:
        plan (str):
        entitlements (GenericObject):
        rights (GenericObject):
        license_ (Union[Unset, LicenseInfo]):
        expiry_status (Union[Unset, str]):
    """

    plan: str
    entitlements: "GenericObject"
    rights: "GenericObject"
    license_: Union[Unset, "LicenseInfo"] = UNSET
    expiry_status: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.license_info import LicenseInfo
        from ..models.generic_object import GenericObject

        plan = self.plan

        entitlements = self.entitlements.to_dict()

        rights = self.rights.to_dict()

        license_: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.license_, Unset):
            license_ = self.license_.to_dict()

        expiry_status = self.expiry_status

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "plan": plan,
                "entitlements": entitlements,
                "rights": rights,
            }
        )
        if license_ is not UNSET:
            field_dict["license"] = license_
        if expiry_status is not UNSET:
            field_dict["expiry_status"] = expiry_status

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.license_info import LicenseInfo
        from ..models.generic_object import GenericObject

        d = src_dict.copy()
        plan = d.pop("plan")

        entitlements = GenericObject.from_dict(d.pop("entitlements"))

        rights = GenericObject.from_dict(d.pop("rights"))

        _license_ = d.pop("license", UNSET)
        license_: Union[Unset, LicenseInfo]
        if isinstance(_license_, Unset):
            license_ = UNSET
        else:
            license_ = LicenseInfo.from_dict(_license_)

        expiry_status = d.pop("expiry_status", UNSET)

        get_license_response_200 = cls(
            plan=plan,
            entitlements=entitlements,
            rights=rights,
            license_=license_,
            expiry_status=expiry_status,
        )

        get_license_response_200.additional_properties = d
        return get_license_response_200

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
