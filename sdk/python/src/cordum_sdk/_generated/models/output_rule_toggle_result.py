from typing import Any, Dict, Type, TypeVar

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field


from dateutil.parser import isoparse
import datetime


T = TypeVar("T", bound="OutputRuleToggleResult")


@_attrs_define
class OutputRuleToggleResult:
    """
    Attributes:
        id (str): The output rule id.
        enabled (bool): The rule's enabled state as written.
        bundle_id (str): Key of the policy bundle that holds the rule and was written.
        updated_at (datetime.datetime): The bundle's new `updated_at` time.
    """

    id: str
    enabled: bool
    bundle_id: str
    updated_at: datetime.datetime
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        id = self.id

        enabled = self.enabled

        bundle_id = self.bundle_id

        updated_at = self.updated_at.isoformat()

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "enabled": enabled,
                "bundle_id": bundle_id,
                "updated_at": updated_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        id = d.pop("id")

        enabled = d.pop("enabled")

        bundle_id = d.pop("bundle_id")

        updated_at = isoparse(d.pop("updated_at"))

        output_rule_toggle_result = cls(
            id=id,
            enabled=enabled,
            bundle_id=bundle_id,
            updated_at=updated_at,
        )

        output_rule_toggle_result.additional_properties = d
        return output_rule_toggle_result

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
