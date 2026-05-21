from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast, List
from typing import Union


T = TypeVar("T", bound="JobRecord")


@_attrs_define
class JobRecord:
    """
    Attributes:
        id (str):
        updated_at (int):
        state (str):
        worker_id (Union[Unset, str]):
        trace_id (Union[Unset, str]):
        topic (Union[Unset, str]):
        tenant (Union[Unset, str]):
        team (Union[Unset, str]):
        principal (Union[Unset, str]):
        actor_id (Union[Unset, str]):
        actor_type (Union[Unset, str]):
        idempotency_key (Union[Unset, str]):
        capability (Union[Unset, str]):
        risk_tags (Union[Unset, List[str]]):
        requires (Union[Unset, List[str]]):
        pack_id (Union[Unset, str]):
        attempts (Union[Unset, int]):
        safety_decision (Union[Unset, str]):
        safety_reason (Union[Unset, str]):
        safety_rule_id (Union[Unset, str]):
        safety_snapshot (Union[Unset, str]):
        deadline_unix (Union[Unset, int]):
        failure_reason (Union[Unset, str]):
    """

    id: str
    updated_at: int
    state: str
    worker_id: Union[Unset, str] = UNSET
    trace_id: Union[Unset, str] = UNSET
    topic: Union[Unset, str] = UNSET
    tenant: Union[Unset, str] = UNSET
    team: Union[Unset, str] = UNSET
    principal: Union[Unset, str] = UNSET
    actor_id: Union[Unset, str] = UNSET
    actor_type: Union[Unset, str] = UNSET
    idempotency_key: Union[Unset, str] = UNSET
    capability: Union[Unset, str] = UNSET
    risk_tags: Union[Unset, List[str]] = UNSET
    requires: Union[Unset, List[str]] = UNSET
    pack_id: Union[Unset, str] = UNSET
    attempts: Union[Unset, int] = UNSET
    safety_decision: Union[Unset, str] = UNSET
    safety_reason: Union[Unset, str] = UNSET
    safety_rule_id: Union[Unset, str] = UNSET
    safety_snapshot: Union[Unset, str] = UNSET
    deadline_unix: Union[Unset, int] = UNSET
    failure_reason: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        id = self.id

        updated_at = self.updated_at

        state = self.state

        worker_id = self.worker_id

        trace_id = self.trace_id

        topic = self.topic

        tenant = self.tenant

        team = self.team

        principal = self.principal

        actor_id = self.actor_id

        actor_type = self.actor_type

        idempotency_key = self.idempotency_key

        capability = self.capability

        risk_tags: Union[Unset, List[str]] = UNSET
        if not isinstance(self.risk_tags, Unset):
            risk_tags = self.risk_tags

        requires: Union[Unset, List[str]] = UNSET
        if not isinstance(self.requires, Unset):
            requires = self.requires

        pack_id = self.pack_id

        attempts = self.attempts

        safety_decision = self.safety_decision

        safety_reason = self.safety_reason

        safety_rule_id = self.safety_rule_id

        safety_snapshot = self.safety_snapshot

        deadline_unix = self.deadline_unix

        failure_reason = self.failure_reason

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "updated_at": updated_at,
                "state": state,
            }
        )
        if worker_id is not UNSET:
            field_dict["worker_id"] = worker_id
        if trace_id is not UNSET:
            field_dict["trace_id"] = trace_id
        if topic is not UNSET:
            field_dict["topic"] = topic
        if tenant is not UNSET:
            field_dict["tenant"] = tenant
        if team is not UNSET:
            field_dict["team"] = team
        if principal is not UNSET:
            field_dict["principal"] = principal
        if actor_id is not UNSET:
            field_dict["actor_id"] = actor_id
        if actor_type is not UNSET:
            field_dict["actor_type"] = actor_type
        if idempotency_key is not UNSET:
            field_dict["idempotency_key"] = idempotency_key
        if capability is not UNSET:
            field_dict["capability"] = capability
        if risk_tags is not UNSET:
            field_dict["risk_tags"] = risk_tags
        if requires is not UNSET:
            field_dict["requires"] = requires
        if pack_id is not UNSET:
            field_dict["pack_id"] = pack_id
        if attempts is not UNSET:
            field_dict["attempts"] = attempts
        if safety_decision is not UNSET:
            field_dict["safety_decision"] = safety_decision
        if safety_reason is not UNSET:
            field_dict["safety_reason"] = safety_reason
        if safety_rule_id is not UNSET:
            field_dict["safety_rule_id"] = safety_rule_id
        if safety_snapshot is not UNSET:
            field_dict["safety_snapshot"] = safety_snapshot
        if deadline_unix is not UNSET:
            field_dict["deadline_unix"] = deadline_unix
        if failure_reason is not UNSET:
            field_dict["failure_reason"] = failure_reason

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        id = d.pop("id")

        updated_at = d.pop("updated_at")

        state = d.pop("state")

        worker_id = d.pop("worker_id", UNSET)

        trace_id = d.pop("trace_id", UNSET)

        topic = d.pop("topic", UNSET)

        tenant = d.pop("tenant", UNSET)

        team = d.pop("team", UNSET)

        principal = d.pop("principal", UNSET)

        actor_id = d.pop("actor_id", UNSET)

        actor_type = d.pop("actor_type", UNSET)

        idempotency_key = d.pop("idempotency_key", UNSET)

        capability = d.pop("capability", UNSET)

        risk_tags = cast(List[str], d.pop("risk_tags", UNSET))

        requires = cast(List[str], d.pop("requires", UNSET))

        pack_id = d.pop("pack_id", UNSET)

        attempts = d.pop("attempts", UNSET)

        safety_decision = d.pop("safety_decision", UNSET)

        safety_reason = d.pop("safety_reason", UNSET)

        safety_rule_id = d.pop("safety_rule_id", UNSET)

        safety_snapshot = d.pop("safety_snapshot", UNSET)

        deadline_unix = d.pop("deadline_unix", UNSET)

        failure_reason = d.pop("failure_reason", UNSET)

        job_record = cls(
            id=id,
            updated_at=updated_at,
            state=state,
            worker_id=worker_id,
            trace_id=trace_id,
            topic=topic,
            tenant=tenant,
            team=team,
            principal=principal,
            actor_id=actor_id,
            actor_type=actor_type,
            idempotency_key=idempotency_key,
            capability=capability,
            risk_tags=risk_tags,
            requires=requires,
            pack_id=pack_id,
            attempts=attempts,
            safety_decision=safety_decision,
            safety_reason=safety_reason,
            safety_rule_id=safety_rule_id,
            safety_snapshot=safety_snapshot,
            deadline_unix=deadline_unix,
            failure_reason=failure_reason,
        )

        job_record.additional_properties = d
        return job_record

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
