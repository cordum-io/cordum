from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import Union


T = TypeVar("T", bound="PolicyGlobalSection")


@_attrs_define
class PolicyGlobalSection:
    """One section of the unified Global policy. The bundle_id is the
    underlying configsvc key (e.g. `secops/global-input`,
    `secops/invariants`). Empty content means no studio overrides
    are authored for this section.

        Attributes:
            bundle_id (Union[Unset, str]):
            content (Union[Unset, str]): YAML SafetyPolicy fragment for this section.
            sha256 (Union[Unset, str]):
            enabled (Union[Unset, bool]):
    """

    bundle_id: Union[Unset, str] = UNSET
    content: Union[Unset, str] = UNSET
    sha256: Union[Unset, str] = UNSET
    enabled: Union[Unset, bool] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        bundle_id = self.bundle_id

        content = self.content

        sha256 = self.sha256

        enabled = self.enabled

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if bundle_id is not UNSET:
            field_dict["bundle_id"] = bundle_id
        if content is not UNSET:
            field_dict["content"] = content
        if sha256 is not UNSET:
            field_dict["sha256"] = sha256
        if enabled is not UNSET:
            field_dict["enabled"] = enabled

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        bundle_id = d.pop("bundle_id", UNSET)

        content = d.pop("content", UNSET)

        sha256 = d.pop("sha256", UNSET)

        enabled = d.pop("enabled", UNSET)

        policy_global_section = cls(
            bundle_id=bundle_id,
            content=content,
            sha256=sha256,
            enabled=enabled,
        )

        policy_global_section.additional_properties = d
        return policy_global_section

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
