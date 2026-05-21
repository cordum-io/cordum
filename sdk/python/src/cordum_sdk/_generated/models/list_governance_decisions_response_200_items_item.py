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
    from ..models.list_governance_decisions_response_200_items_item_constraints import (
        ListGovernanceDecisionsResponse200ItemsItemConstraints,
    )


T = TypeVar("T", bound="ListGovernanceDecisionsResponse200ItemsItem")


@_attrs_define
class ListGovernanceDecisionsResponse200ItemsItem:
    """
    Attributes:
        job_id (Union[Unset, str]):
        topic (Union[Unset, str]):
        matched_rule (Union[Unset, str]):
        verdict (Union[Unset, str]):
        reason (Union[Unset, str]):
        constraints (Union[Unset, ListGovernanceDecisionsResponse200ItemsItemConstraints]):
        approval_status (Union[Unset, str]):
        approval_decision (Union[Unset, str]):
        agent_id (Union[Unset, str]):
        policy_version (Union[Unset, str]):
        timestamp (Union[Unset, str]):
    """

    job_id: Union[Unset, str] = UNSET
    topic: Union[Unset, str] = UNSET
    matched_rule: Union[Unset, str] = UNSET
    verdict: Union[Unset, str] = UNSET
    reason: Union[Unset, str] = UNSET
    constraints: Union[Unset, "ListGovernanceDecisionsResponse200ItemsItemConstraints"] = UNSET
    approval_status: Union[Unset, str] = UNSET
    approval_decision: Union[Unset, str] = UNSET
    agent_id: Union[Unset, str] = UNSET
    policy_version: Union[Unset, str] = UNSET
    timestamp: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.list_governance_decisions_response_200_items_item_constraints import (
            ListGovernanceDecisionsResponse200ItemsItemConstraints,
        )

        job_id = self.job_id

        topic = self.topic

        matched_rule = self.matched_rule

        verdict = self.verdict

        reason = self.reason

        constraints: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.constraints, Unset):
            constraints = self.constraints.to_dict()

        approval_status = self.approval_status

        approval_decision = self.approval_decision

        agent_id = self.agent_id

        policy_version = self.policy_version

        timestamp = self.timestamp

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if job_id is not UNSET:
            field_dict["job_id"] = job_id
        if topic is not UNSET:
            field_dict["topic"] = topic
        if matched_rule is not UNSET:
            field_dict["matched_rule"] = matched_rule
        if verdict is not UNSET:
            field_dict["verdict"] = verdict
        if reason is not UNSET:
            field_dict["reason"] = reason
        if constraints is not UNSET:
            field_dict["constraints"] = constraints
        if approval_status is not UNSET:
            field_dict["approval_status"] = approval_status
        if approval_decision is not UNSET:
            field_dict["approval_decision"] = approval_decision
        if agent_id is not UNSET:
            field_dict["agent_id"] = agent_id
        if policy_version is not UNSET:
            field_dict["policy_version"] = policy_version
        if timestamp is not UNSET:
            field_dict["timestamp"] = timestamp

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.list_governance_decisions_response_200_items_item_constraints import (
            ListGovernanceDecisionsResponse200ItemsItemConstraints,
        )

        d = src_dict.copy()
        job_id = d.pop("job_id", UNSET)

        topic = d.pop("topic", UNSET)

        matched_rule = d.pop("matched_rule", UNSET)

        verdict = d.pop("verdict", UNSET)

        reason = d.pop("reason", UNSET)

        _constraints = d.pop("constraints", UNSET)
        constraints: Union[Unset, ListGovernanceDecisionsResponse200ItemsItemConstraints]
        if isinstance(_constraints, Unset):
            constraints = UNSET
        else:
            constraints = ListGovernanceDecisionsResponse200ItemsItemConstraints.from_dict(
                _constraints
            )

        approval_status = d.pop("approval_status", UNSET)

        approval_decision = d.pop("approval_decision", UNSET)

        agent_id = d.pop("agent_id", UNSET)

        policy_version = d.pop("policy_version", UNSET)

        timestamp = d.pop("timestamp", UNSET)

        list_governance_decisions_response_200_items_item = cls(
            job_id=job_id,
            topic=topic,
            matched_rule=matched_rule,
            verdict=verdict,
            reason=reason,
            constraints=constraints,
            approval_status=approval_status,
            approval_decision=approval_decision,
            agent_id=agent_id,
            policy_version=policy_version,
            timestamp=timestamp,
        )

        list_governance_decisions_response_200_items_item.additional_properties = d
        return list_governance_decisions_response_200_items_item

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
