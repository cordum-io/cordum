from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset


T = TypeVar("T", bound="PolicyReplayResponseRuleHitsItem")


@_attrs_define
class PolicyReplayResponseRuleHitsItem:
    """
    Attributes:
        rule_id (str):
        decision (str):
        count (int):
    """

    rule_id: str
    decision: str
    count: int
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        rule_id = self.rule_id

        decision = self.decision

        count = self.count

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "rule_id": rule_id,
                "decision": decision,
                "count": count,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        rule_id = d.pop("rule_id")

        decision = d.pop("decision")

        count = d.pop("count")

        policy_replay_response_rule_hits_item = cls(
            rule_id=rule_id,
            decision=decision,
            count=count,
        )

        policy_replay_response_rule_hits_item.additional_properties = d
        return policy_replay_response_rule_hits_item

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
