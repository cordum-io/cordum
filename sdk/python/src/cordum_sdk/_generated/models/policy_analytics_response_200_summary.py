from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import Union


T = TypeVar("T", bound="PolicyAnalyticsResponse200Summary")


@_attrs_define
class PolicyAnalyticsResponse200Summary:
    """
    Attributes:
        total_rules (Union[Unset, int]):
        total_hits (Union[Unset, int]):
        total_overrides (Union[Unset, int]):
        highest_override_rule_id (Union[Unset, str]):
    """

    total_rules: Union[Unset, int] = UNSET
    total_hits: Union[Unset, int] = UNSET
    total_overrides: Union[Unset, int] = UNSET
    highest_override_rule_id: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        total_rules = self.total_rules

        total_hits = self.total_hits

        total_overrides = self.total_overrides

        highest_override_rule_id = self.highest_override_rule_id

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if total_rules is not UNSET:
            field_dict["total_rules"] = total_rules
        if total_hits is not UNSET:
            field_dict["total_hits"] = total_hits
        if total_overrides is not UNSET:
            field_dict["total_overrides"] = total_overrides
        if highest_override_rule_id is not UNSET:
            field_dict["highest_override_rule_id"] = highest_override_rule_id

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        total_rules = d.pop("total_rules", UNSET)

        total_hits = d.pop("total_hits", UNSET)

        total_overrides = d.pop("total_overrides", UNSET)

        highest_override_rule_id = d.pop("highest_override_rule_id", UNSET)

        policy_analytics_response_200_summary = cls(
            total_rules=total_rules,
            total_hits=total_hits,
            total_overrides=total_overrides,
            highest_override_rule_id=highest_override_rule_id,
        )

        policy_analytics_response_200_summary.additional_properties = d
        return policy_analytics_response_200_summary

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
