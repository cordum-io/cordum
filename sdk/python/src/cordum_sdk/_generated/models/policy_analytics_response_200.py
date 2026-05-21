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
    from ..models.policy_analytics_response_200_summary import PolicyAnalyticsResponse200Summary
    from ..models.policy_analytics_response_200_rules_item import (
        PolicyAnalyticsResponse200RulesItem,
    )
    from ..models.policy_analytics_response_200_time_range import (
        PolicyAnalyticsResponse200TimeRange,
    )


T = TypeVar("T", bound="PolicyAnalyticsResponse200")


@_attrs_define
class PolicyAnalyticsResponse200:
    """
    Attributes:
        time_range (Union[Unset, PolicyAnalyticsResponse200TimeRange]):
        rules (Union[Unset, List['PolicyAnalyticsResponse200RulesItem']]):
        summary (Union[Unset, PolicyAnalyticsResponse200Summary]):
    """

    time_range: Union[Unset, "PolicyAnalyticsResponse200TimeRange"] = UNSET
    rules: Union[Unset, List["PolicyAnalyticsResponse200RulesItem"]] = UNSET
    summary: Union[Unset, "PolicyAnalyticsResponse200Summary"] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.policy_analytics_response_200_summary import PolicyAnalyticsResponse200Summary
        from ..models.policy_analytics_response_200_rules_item import (
            PolicyAnalyticsResponse200RulesItem,
        )
        from ..models.policy_analytics_response_200_time_range import (
            PolicyAnalyticsResponse200TimeRange,
        )

        time_range: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.time_range, Unset):
            time_range = self.time_range.to_dict()

        rules: Union[Unset, List[Dict[str, Any]]] = UNSET
        if not isinstance(self.rules, Unset):
            rules = []
            for rules_item_data in self.rules:
                rules_item = rules_item_data.to_dict()
                rules.append(rules_item)

        summary: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.summary, Unset):
            summary = self.summary.to_dict()

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if time_range is not UNSET:
            field_dict["time_range"] = time_range
        if rules is not UNSET:
            field_dict["rules"] = rules
        if summary is not UNSET:
            field_dict["summary"] = summary

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.policy_analytics_response_200_summary import PolicyAnalyticsResponse200Summary
        from ..models.policy_analytics_response_200_rules_item import (
            PolicyAnalyticsResponse200RulesItem,
        )
        from ..models.policy_analytics_response_200_time_range import (
            PolicyAnalyticsResponse200TimeRange,
        )

        d = src_dict.copy()
        _time_range = d.pop("time_range", UNSET)
        time_range: Union[Unset, PolicyAnalyticsResponse200TimeRange]
        if isinstance(_time_range, Unset):
            time_range = UNSET
        else:
            time_range = PolicyAnalyticsResponse200TimeRange.from_dict(_time_range)

        rules = []
        _rules = d.pop("rules", UNSET)
        for rules_item_data in _rules or []:
            rules_item = PolicyAnalyticsResponse200RulesItem.from_dict(rules_item_data)

            rules.append(rules_item)

        _summary = d.pop("summary", UNSET)
        summary: Union[Unset, PolicyAnalyticsResponse200Summary]
        if isinstance(_summary, Unset):
            summary = UNSET
        else:
            summary = PolicyAnalyticsResponse200Summary.from_dict(_summary)

        policy_analytics_response_200 = cls(
            time_range=time_range,
            rules=rules,
            summary=summary,
        )

        policy_analytics_response_200.additional_properties = d
        return policy_analytics_response_200

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
