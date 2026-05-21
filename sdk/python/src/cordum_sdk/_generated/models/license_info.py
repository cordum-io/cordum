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
    from ..models.license_info_limits import LicenseInfoLimits


T = TypeVar("T", bound="LicenseInfo")


@_attrs_define
class LicenseInfo:
    """
    Attributes:
        mode (Union[Unset, str]):
        status (Union[Unset, str]):
        plan (Union[Unset, str]):
        org_id (Union[Unset, str]):
        license_id (Union[Unset, str]):
        deployment_type (Union[Unset, str]):
        issued_at (Union[Unset, str]):
        not_before (Union[Unset, str]):
        expires_at (Union[Unset, str]):
        features (Union[Unset, List[str]]):
        limits (Union[Unset, LicenseInfoLimits]):
    """

    mode: Union[Unset, str] = UNSET
    status: Union[Unset, str] = UNSET
    plan: Union[Unset, str] = UNSET
    org_id: Union[Unset, str] = UNSET
    license_id: Union[Unset, str] = UNSET
    deployment_type: Union[Unset, str] = UNSET
    issued_at: Union[Unset, str] = UNSET
    not_before: Union[Unset, str] = UNSET
    expires_at: Union[Unset, str] = UNSET
    features: Union[Unset, List[str]] = UNSET
    limits: Union[Unset, "LicenseInfoLimits"] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.license_info_limits import LicenseInfoLimits

        mode = self.mode

        status = self.status

        plan = self.plan

        org_id = self.org_id

        license_id = self.license_id

        deployment_type = self.deployment_type

        issued_at = self.issued_at

        not_before = self.not_before

        expires_at = self.expires_at

        features: Union[Unset, List[str]] = UNSET
        if not isinstance(self.features, Unset):
            features = self.features

        limits: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.limits, Unset):
            limits = self.limits.to_dict()

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if mode is not UNSET:
            field_dict["mode"] = mode
        if status is not UNSET:
            field_dict["status"] = status
        if plan is not UNSET:
            field_dict["plan"] = plan
        if org_id is not UNSET:
            field_dict["org_id"] = org_id
        if license_id is not UNSET:
            field_dict["license_id"] = license_id
        if deployment_type is not UNSET:
            field_dict["deployment_type"] = deployment_type
        if issued_at is not UNSET:
            field_dict["issued_at"] = issued_at
        if not_before is not UNSET:
            field_dict["not_before"] = not_before
        if expires_at is not UNSET:
            field_dict["expires_at"] = expires_at
        if features is not UNSET:
            field_dict["features"] = features
        if limits is not UNSET:
            field_dict["limits"] = limits

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.license_info_limits import LicenseInfoLimits

        d = src_dict.copy()
        mode = d.pop("mode", UNSET)

        status = d.pop("status", UNSET)

        plan = d.pop("plan", UNSET)

        org_id = d.pop("org_id", UNSET)

        license_id = d.pop("license_id", UNSET)

        deployment_type = d.pop("deployment_type", UNSET)

        issued_at = d.pop("issued_at", UNSET)

        not_before = d.pop("not_before", UNSET)

        expires_at = d.pop("expires_at", UNSET)

        features = cast(List[str], d.pop("features", UNSET))

        _limits = d.pop("limits", UNSET)
        limits: Union[Unset, LicenseInfoLimits]
        if isinstance(_limits, Unset):
            limits = UNSET
        else:
            limits = LicenseInfoLimits.from_dict(_limits)

        license_info = cls(
            mode=mode,
            status=status,
            plan=plan,
            org_id=org_id,
            license_id=license_id,
            deployment_type=deployment_type,
            issued_at=issued_at,
            not_before=not_before,
            expires_at=expires_at,
            features=features,
            limits=limits,
        )

        license_info.additional_properties = d
        return license_info

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
