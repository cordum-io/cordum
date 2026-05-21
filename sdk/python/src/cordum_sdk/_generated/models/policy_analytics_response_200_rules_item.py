from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast, List
from typing import Union


T = TypeVar("T", bound="PolicyAnalyticsResponse200RulesItem")


@_attrs_define
class PolicyAnalyticsResponse200RulesItem:
    """
    Attributes:
        rule_id (Union[Unset, str]):
        hit_count (Union[Unset, int]):
        approval_count (Union[Unset, int]):
        override_count (Union[Unset, int]):
        override_rate (Union[Unset, float]): override_count / approval_count (0 if no approvals)
        avg_approval_latency_ms (Union[Unset, int]):
        daily_hits (Union[Unset, List[int]]): Per-day hit counts (index 0 = oldest day)
    """

    rule_id: Union[Unset, str] = UNSET
    hit_count: Union[Unset, int] = UNSET
    approval_count: Union[Unset, int] = UNSET
    override_count: Union[Unset, int] = UNSET
    override_rate: Union[Unset, float] = UNSET
    avg_approval_latency_ms: Union[Unset, int] = UNSET
    daily_hits: Union[Unset, List[int]] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        rule_id = self.rule_id

        hit_count = self.hit_count

        approval_count = self.approval_count

        override_count = self.override_count

        override_rate = self.override_rate

        avg_approval_latency_ms = self.avg_approval_latency_ms

        daily_hits: Union[Unset, List[int]] = UNSET
        if not isinstance(self.daily_hits, Unset):
            daily_hits = self.daily_hits

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if rule_id is not UNSET:
            field_dict["rule_id"] = rule_id
        if hit_count is not UNSET:
            field_dict["hit_count"] = hit_count
        if approval_count is not UNSET:
            field_dict["approval_count"] = approval_count
        if override_count is not UNSET:
            field_dict["override_count"] = override_count
        if override_rate is not UNSET:
            field_dict["override_rate"] = override_rate
        if avg_approval_latency_ms is not UNSET:
            field_dict["avg_approval_latency_ms"] = avg_approval_latency_ms
        if daily_hits is not UNSET:
            field_dict["daily_hits"] = daily_hits

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        rule_id = d.pop("rule_id", UNSET)

        hit_count = d.pop("hit_count", UNSET)

        approval_count = d.pop("approval_count", UNSET)

        override_count = d.pop("override_count", UNSET)

        override_rate = d.pop("override_rate", UNSET)

        avg_approval_latency_ms = d.pop("avg_approval_latency_ms", UNSET)

        daily_hits = cast(List[int], d.pop("daily_hits", UNSET))

        policy_analytics_response_200_rules_item = cls(
            rule_id=rule_id,
            hit_count=hit_count,
            approval_count=approval_count,
            override_count=override_count,
            override_rate=override_rate,
            avg_approval_latency_ms=avg_approval_latency_ms,
            daily_hits=daily_hits,
        )

        policy_analytics_response_200_rules_item.additional_properties = d
        return policy_analytics_response_200_rules_item

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
