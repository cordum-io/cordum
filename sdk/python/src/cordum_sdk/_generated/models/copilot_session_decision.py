from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import cast, Union
from typing import Dict
from typing import Union
import datetime

if TYPE_CHECKING:
    from ..models.copilot_session_decision_constraints_type_0 import (
        CopilotSessionDecisionConstraintsType0,
    )


T = TypeVar("T", bound="CopilotSessionDecision")


@_attrs_define
class CopilotSessionDecision:
    """
    Attributes:
        job_id (str):
        verdict (str):
        timestamp (datetime.datetime):
        topic (Union[Unset, str]):
        matched_rule (Union[Unset, str]):
        rule_name (Union[Unset, str]):
        reason (Union[Unset, str]):
        constraints (Union['CopilotSessionDecisionConstraintsType0', None, Unset]):
        approval_status (Union[Unset, str]):
        approval_decision (Union[Unset, str]):
        agent_id (Union[Unset, str]):
        policy_version (Union[Unset, str]):
    """

    job_id: str
    verdict: str
    timestamp: datetime.datetime
    topic: Union[Unset, str] = UNSET
    matched_rule: Union[Unset, str] = UNSET
    rule_name: Union[Unset, str] = UNSET
    reason: Union[Unset, str] = UNSET
    constraints: Union["CopilotSessionDecisionConstraintsType0", None, Unset] = UNSET
    approval_status: Union[Unset, str] = UNSET
    approval_decision: Union[Unset, str] = UNSET
    agent_id: Union[Unset, str] = UNSET
    policy_version: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.copilot_session_decision_constraints_type_0 import (
            CopilotSessionDecisionConstraintsType0,
        )

        job_id = self.job_id

        verdict = self.verdict

        timestamp = self.timestamp.isoformat()

        topic = self.topic

        matched_rule = self.matched_rule

        rule_name = self.rule_name

        reason = self.reason

        constraints: Union[Dict[str, Any], None, Unset]
        if isinstance(self.constraints, Unset):
            constraints = UNSET
        elif isinstance(self.constraints, CopilotSessionDecisionConstraintsType0):
            constraints = self.constraints.to_dict()
        else:
            constraints = self.constraints

        approval_status = self.approval_status

        approval_decision = self.approval_decision

        agent_id = self.agent_id

        policy_version = self.policy_version

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "jobId": job_id,
                "verdict": verdict,
                "timestamp": timestamp,
            }
        )
        if topic is not UNSET:
            field_dict["topic"] = topic
        if matched_rule is not UNSET:
            field_dict["matchedRule"] = matched_rule
        if rule_name is not UNSET:
            field_dict["ruleName"] = rule_name
        if reason is not UNSET:
            field_dict["reason"] = reason
        if constraints is not UNSET:
            field_dict["constraints"] = constraints
        if approval_status is not UNSET:
            field_dict["approvalStatus"] = approval_status
        if approval_decision is not UNSET:
            field_dict["approvalDecision"] = approval_decision
        if agent_id is not UNSET:
            field_dict["agentId"] = agent_id
        if policy_version is not UNSET:
            field_dict["policyVersion"] = policy_version

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.copilot_session_decision_constraints_type_0 import (
            CopilotSessionDecisionConstraintsType0,
        )

        d = src_dict.copy()
        job_id = d.pop("jobId")

        verdict = d.pop("verdict")

        timestamp = isoparse(d.pop("timestamp"))

        topic = d.pop("topic", UNSET)

        matched_rule = d.pop("matchedRule", UNSET)

        rule_name = d.pop("ruleName", UNSET)

        reason = d.pop("reason", UNSET)

        def _parse_constraints(
            data: object,
        ) -> Union["CopilotSessionDecisionConstraintsType0", None, Unset]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                constraints_type_0 = CopilotSessionDecisionConstraintsType0.from_dict(data)

                return constraints_type_0
            except:  # noqa: E722
                pass
            return cast(Union["CopilotSessionDecisionConstraintsType0", None, Unset], data)

        constraints = _parse_constraints(d.pop("constraints", UNSET))

        approval_status = d.pop("approvalStatus", UNSET)

        approval_decision = d.pop("approvalDecision", UNSET)

        agent_id = d.pop("agentId", UNSET)

        policy_version = d.pop("policyVersion", UNSET)

        copilot_session_decision = cls(
            job_id=job_id,
            verdict=verdict,
            timestamp=timestamp,
            topic=topic,
            matched_rule=matched_rule,
            rule_name=rule_name,
            reason=reason,
            constraints=constraints,
            approval_status=approval_status,
            approval_decision=approval_decision,
            agent_id=agent_id,
            policy_version=policy_version,
        )

        copilot_session_decision.additional_properties = d
        return copilot_session_decision

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
