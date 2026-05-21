from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import Union


T = TypeVar("T", bound="PolicyReplayResponseChangesItem")


@_attrs_define
class PolicyReplayResponseChangesItem:
    """
    Attributes:
        job_id (str):
        topic (str):
        tenant (str):
        original_decision (str):
        new_decision (str):
        direction (str):
        new_rule_id (Union[Unset, str]):
        new_reason (Union[Unset, str]):
    """

    job_id: str
    topic: str
    tenant: str
    original_decision: str
    new_decision: str
    direction: str
    new_rule_id: Union[Unset, str] = UNSET
    new_reason: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        job_id = self.job_id

        topic = self.topic

        tenant = self.tenant

        original_decision = self.original_decision

        new_decision = self.new_decision

        direction = self.direction

        new_rule_id = self.new_rule_id

        new_reason = self.new_reason

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "job_id": job_id,
                "topic": topic,
                "tenant": tenant,
                "original_decision": original_decision,
                "new_decision": new_decision,
                "direction": direction,
            }
        )
        if new_rule_id is not UNSET:
            field_dict["new_rule_id"] = new_rule_id
        if new_reason is not UNSET:
            field_dict["new_reason"] = new_reason

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        job_id = d.pop("job_id")

        topic = d.pop("topic")

        tenant = d.pop("tenant")

        original_decision = d.pop("original_decision")

        new_decision = d.pop("new_decision")

        direction = d.pop("direction")

        new_rule_id = d.pop("new_rule_id", UNSET)

        new_reason = d.pop("new_reason", UNSET)

        policy_replay_response_changes_item = cls(
            job_id=job_id,
            topic=topic,
            tenant=tenant,
            original_decision=original_decision,
            new_decision=new_decision,
            direction=direction,
            new_rule_id=new_rule_id,
            new_reason=new_reason,
        )

        policy_replay_response_changes_item.additional_properties = d
        return policy_replay_response_changes_item

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
