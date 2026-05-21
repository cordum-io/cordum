from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import cast, List
from typing import Dict
from typing import Union
import datetime

if TYPE_CHECKING:
    from ..models.audit_event_extra import AuditEventExtra


T = TypeVar("T", bound="AuditEvent")


@_attrs_define
class AuditEvent:
    """One SIEM audit event returned by `GET /api/v1/audit/events`. Mirrors
    the gateway handler's `auditEventResponseItem` (which itself mirrors
    `audit.SIEMEvent` in `core/audit/exporter.go`). The `extra` map's
    secret-shaped keys (token / password / api_key / private_key / secret)
    are stripped server-side as defense-in-depth before the response is
    serialized; their absence does NOT imply the source event lacked them.

        Attributes:
            id (str): Opaque per-event handle (Redis Stream message ID under the hood).
                Stable across pagination — safe to use as a row key.
            seq (int): Per-tenant monotonic chain sequence. First event for a tenant has
                seq=1. Pairs with `/api/v1/audit/verify` for forensic re-check.
            timestamp (datetime.datetime):
            event_type (str):
            severity (str): One of CRITICAL / HIGH / MEDIUM / LOW / INFO.
            tenant_id (str):
            action (str):
            agent_id (Union[Unset, str]):
            agent_name (Union[Unset, str]):
            agent_risk_tier (Union[Unset, str]):
            job_id (Union[Unset, str]):
            decision (Union[Unset, str]):
            matched_rule (Union[Unset, str]):
            reason (Union[Unset, str]):
            risk_tags (Union[Unset, List[str]]):
            capabilities (Union[Unset, List[str]]):
            policy_version (Union[Unset, str]):
            identity (Union[Unset, str]): Identity of the actor who produced the event.
            extra (Union[Unset, AuditEventExtra]): Free-form per-event metadata. Secret-shaped keys are stripped
                before serialization (see schema-level description).
            event_hash (Union[Unset, str]): SHA-256 of the canonical event payload, hex-encoded.
            prev_hash (Union[Unset, str]): EventHash of the tenant's predecessor event, or empty for genesis.
    """

    id: str
    seq: int
    timestamp: datetime.datetime
    event_type: str
    severity: str
    tenant_id: str
    action: str
    agent_id: Union[Unset, str] = UNSET
    agent_name: Union[Unset, str] = UNSET
    agent_risk_tier: Union[Unset, str] = UNSET
    job_id: Union[Unset, str] = UNSET
    decision: Union[Unset, str] = UNSET
    matched_rule: Union[Unset, str] = UNSET
    reason: Union[Unset, str] = UNSET
    risk_tags: Union[Unset, List[str]] = UNSET
    capabilities: Union[Unset, List[str]] = UNSET
    policy_version: Union[Unset, str] = UNSET
    identity: Union[Unset, str] = UNSET
    extra: Union[Unset, "AuditEventExtra"] = UNSET
    event_hash: Union[Unset, str] = UNSET
    prev_hash: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.audit_event_extra import AuditEventExtra

        id = self.id

        seq = self.seq

        timestamp = self.timestamp.isoformat()

        event_type = self.event_type

        severity = self.severity

        tenant_id = self.tenant_id

        action = self.action

        agent_id = self.agent_id

        agent_name = self.agent_name

        agent_risk_tier = self.agent_risk_tier

        job_id = self.job_id

        decision = self.decision

        matched_rule = self.matched_rule

        reason = self.reason

        risk_tags: Union[Unset, List[str]] = UNSET
        if not isinstance(self.risk_tags, Unset):
            risk_tags = self.risk_tags

        capabilities: Union[Unset, List[str]] = UNSET
        if not isinstance(self.capabilities, Unset):
            capabilities = self.capabilities

        policy_version = self.policy_version

        identity = self.identity

        extra: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.extra, Unset):
            extra = self.extra.to_dict()

        event_hash = self.event_hash

        prev_hash = self.prev_hash

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "seq": seq,
                "timestamp": timestamp,
                "event_type": event_type,
                "severity": severity,
                "tenant_id": tenant_id,
                "action": action,
            }
        )
        if agent_id is not UNSET:
            field_dict["agent_id"] = agent_id
        if agent_name is not UNSET:
            field_dict["agent_name"] = agent_name
        if agent_risk_tier is not UNSET:
            field_dict["agent_risk_tier"] = agent_risk_tier
        if job_id is not UNSET:
            field_dict["job_id"] = job_id
        if decision is not UNSET:
            field_dict["decision"] = decision
        if matched_rule is not UNSET:
            field_dict["matched_rule"] = matched_rule
        if reason is not UNSET:
            field_dict["reason"] = reason
        if risk_tags is not UNSET:
            field_dict["risk_tags"] = risk_tags
        if capabilities is not UNSET:
            field_dict["capabilities"] = capabilities
        if policy_version is not UNSET:
            field_dict["policy_version"] = policy_version
        if identity is not UNSET:
            field_dict["identity"] = identity
        if extra is not UNSET:
            field_dict["extra"] = extra
        if event_hash is not UNSET:
            field_dict["event_hash"] = event_hash
        if prev_hash is not UNSET:
            field_dict["prev_hash"] = prev_hash

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.audit_event_extra import AuditEventExtra

        d = src_dict.copy()
        id = d.pop("id")

        seq = d.pop("seq")

        timestamp = isoparse(d.pop("timestamp"))

        event_type = d.pop("event_type")

        severity = d.pop("severity")

        tenant_id = d.pop("tenant_id")

        action = d.pop("action")

        agent_id = d.pop("agent_id", UNSET)

        agent_name = d.pop("agent_name", UNSET)

        agent_risk_tier = d.pop("agent_risk_tier", UNSET)

        job_id = d.pop("job_id", UNSET)

        decision = d.pop("decision", UNSET)

        matched_rule = d.pop("matched_rule", UNSET)

        reason = d.pop("reason", UNSET)

        risk_tags = cast(List[str], d.pop("risk_tags", UNSET))

        capabilities = cast(List[str], d.pop("capabilities", UNSET))

        policy_version = d.pop("policy_version", UNSET)

        identity = d.pop("identity", UNSET)

        _extra = d.pop("extra", UNSET)
        extra: Union[Unset, AuditEventExtra]
        if isinstance(_extra, Unset):
            extra = UNSET
        else:
            extra = AuditEventExtra.from_dict(_extra)

        event_hash = d.pop("event_hash", UNSET)

        prev_hash = d.pop("prev_hash", UNSET)

        audit_event = cls(
            id=id,
            seq=seq,
            timestamp=timestamp,
            event_type=event_type,
            severity=severity,
            tenant_id=tenant_id,
            action=action,
            agent_id=agent_id,
            agent_name=agent_name,
            agent_risk_tier=agent_risk_tier,
            job_id=job_id,
            decision=decision,
            matched_rule=matched_rule,
            reason=reason,
            risk_tags=risk_tags,
            capabilities=capabilities,
            policy_version=policy_version,
            identity=identity,
            extra=extra,
            event_hash=event_hash,
            prev_hash=prev_hash,
        )

        audit_event.additional_properties = d
        return audit_event

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
