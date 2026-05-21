from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.edge_approval_decision import EdgeApprovalDecision
from ..models.edge_approval_status import EdgeApprovalStatus
from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import cast, Union
from typing import Dict
from typing import Union
import datetime

if TYPE_CHECKING:
    from ..models.edge_approval_metadata import EdgeApprovalMetadata
    from ..models.edge_labels import EdgeLabels


T = TypeVar("T", bound="EdgeApproval")


@_attrs_define
class EdgeApproval:
    """Tenant-scoped action approval bound to an Edge session/execution/event, action hash, and policy snapshot. Raw
    action/tool payloads are not stored here; use hashes and artifact pointers for evidence.

        Attributes:
            approval_ref (str):
            tenant_id (str):
            session_id (str):
            execution_id (str):
            event_id (str):
            principal_id (str): Principal that requested the governed action.
            requester (str): Authenticated requester identity used for self-approval protection.
            status (EdgeApprovalStatus):
            reason (str): Trigger reason explaining why approval was required.
            rule_id (str):
            policy_snapshot (str): Redacted policy snapshot identifier; used to reject stale approvals.
            action_hash (str): Stable action hash. The raw action payload is not stored.
            input_hash (str): Stable hash of the redacted/bounded action input used for evidence correlation.
            created_at (datetime.datetime):
            expires_at (Union[None, datetime.datetime]):
            resolver_id (Union[Unset, str]): Principal that approved/rejected/expired the approval.
            resolved_by (Union[Unset, str]): Display/audit identity for the resolver.
            decision (Union[Unset, EdgeApprovalDecision]): Empty while pending; terminal decisions match status.
            resolution_reason (Union[Unset, str]): Resolver-provided reason or system expiry reason for terminal records.
            resolved_at (Union[None, Unset, datetime.datetime]):
            consumed_at (Union[None, Unset, datetime.datetime]): Set once an approved approval is atomically claimed by the
                agent retry path.
            labels (Union[Unset, EdgeLabels]):
            metadata (Union[Unset, EdgeApprovalMetadata]):
    """

    approval_ref: str
    tenant_id: str
    session_id: str
    execution_id: str
    event_id: str
    principal_id: str
    requester: str
    status: EdgeApprovalStatus
    reason: str
    rule_id: str
    policy_snapshot: str
    action_hash: str
    input_hash: str
    created_at: datetime.datetime
    expires_at: Union[None, datetime.datetime]
    resolver_id: Union[Unset, str] = UNSET
    resolved_by: Union[Unset, str] = UNSET
    decision: Union[Unset, EdgeApprovalDecision] = UNSET
    resolution_reason: Union[Unset, str] = UNSET
    resolved_at: Union[None, Unset, datetime.datetime] = UNSET
    consumed_at: Union[None, Unset, datetime.datetime] = UNSET
    labels: Union[Unset, "EdgeLabels"] = UNSET
    metadata: Union[Unset, "EdgeApprovalMetadata"] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.edge_approval_metadata import EdgeApprovalMetadata
        from ..models.edge_labels import EdgeLabels

        approval_ref = self.approval_ref

        tenant_id = self.tenant_id

        session_id = self.session_id

        execution_id = self.execution_id

        event_id = self.event_id

        principal_id = self.principal_id

        requester = self.requester

        status = self.status.value

        reason = self.reason

        rule_id = self.rule_id

        policy_snapshot = self.policy_snapshot

        action_hash = self.action_hash

        input_hash = self.input_hash

        created_at = self.created_at.isoformat()

        expires_at: Union[None, str]
        if isinstance(self.expires_at, datetime.datetime):
            expires_at = self.expires_at.isoformat()
        else:
            expires_at = self.expires_at

        resolver_id = self.resolver_id

        resolved_by = self.resolved_by

        decision: Union[Unset, str] = UNSET
        if not isinstance(self.decision, Unset):
            decision = self.decision.value

        resolution_reason = self.resolution_reason

        resolved_at: Union[None, Unset, str]
        if isinstance(self.resolved_at, Unset):
            resolved_at = UNSET
        elif isinstance(self.resolved_at, datetime.datetime):
            resolved_at = self.resolved_at.isoformat()
        else:
            resolved_at = self.resolved_at

        consumed_at: Union[None, Unset, str]
        if isinstance(self.consumed_at, Unset):
            consumed_at = UNSET
        elif isinstance(self.consumed_at, datetime.datetime):
            consumed_at = self.consumed_at.isoformat()
        else:
            consumed_at = self.consumed_at

        labels: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.labels, Unset):
            labels = self.labels.to_dict()

        metadata: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.metadata, Unset):
            metadata = self.metadata.to_dict()

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "approval_ref": approval_ref,
                "tenant_id": tenant_id,
                "session_id": session_id,
                "execution_id": execution_id,
                "event_id": event_id,
                "principal_id": principal_id,
                "requester": requester,
                "status": status,
                "reason": reason,
                "rule_id": rule_id,
                "policy_snapshot": policy_snapshot,
                "action_hash": action_hash,
                "input_hash": input_hash,
                "created_at": created_at,
                "expires_at": expires_at,
            }
        )
        if resolver_id is not UNSET:
            field_dict["resolver_id"] = resolver_id
        if resolved_by is not UNSET:
            field_dict["resolved_by"] = resolved_by
        if decision is not UNSET:
            field_dict["decision"] = decision
        if resolution_reason is not UNSET:
            field_dict["resolution_reason"] = resolution_reason
        if resolved_at is not UNSET:
            field_dict["resolved_at"] = resolved_at
        if consumed_at is not UNSET:
            field_dict["consumed_at"] = consumed_at
        if labels is not UNSET:
            field_dict["labels"] = labels
        if metadata is not UNSET:
            field_dict["metadata"] = metadata

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.edge_approval_metadata import EdgeApprovalMetadata
        from ..models.edge_labels import EdgeLabels

        d = src_dict.copy()
        approval_ref = d.pop("approval_ref")

        tenant_id = d.pop("tenant_id")

        session_id = d.pop("session_id")

        execution_id = d.pop("execution_id")

        event_id = d.pop("event_id")

        principal_id = d.pop("principal_id")

        requester = d.pop("requester")

        status = EdgeApprovalStatus(d.pop("status"))

        reason = d.pop("reason")

        rule_id = d.pop("rule_id")

        policy_snapshot = d.pop("policy_snapshot")

        action_hash = d.pop("action_hash")

        input_hash = d.pop("input_hash")

        created_at = isoparse(d.pop("created_at"))

        def _parse_expires_at(data: object) -> Union[None, datetime.datetime]:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                expires_at_type_0 = isoparse(data)

                return expires_at_type_0
            except:  # noqa: E722
                pass
            return cast(Union[None, datetime.datetime], data)

        expires_at = _parse_expires_at(d.pop("expires_at"))

        resolver_id = d.pop("resolver_id", UNSET)

        resolved_by = d.pop("resolved_by", UNSET)

        _decision = d.pop("decision", UNSET)
        decision: Union[Unset, EdgeApprovalDecision]
        if isinstance(_decision, Unset):
            decision = UNSET
        else:
            decision = EdgeApprovalDecision(_decision)

        resolution_reason = d.pop("resolution_reason", UNSET)

        def _parse_resolved_at(data: object) -> Union[None, Unset, datetime.datetime]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                resolved_at_type_0 = isoparse(data)

                return resolved_at_type_0
            except:  # noqa: E722
                pass
            return cast(Union[None, Unset, datetime.datetime], data)

        resolved_at = _parse_resolved_at(d.pop("resolved_at", UNSET))

        def _parse_consumed_at(data: object) -> Union[None, Unset, datetime.datetime]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                consumed_at_type_0 = isoparse(data)

                return consumed_at_type_0
            except:  # noqa: E722
                pass
            return cast(Union[None, Unset, datetime.datetime], data)

        consumed_at = _parse_consumed_at(d.pop("consumed_at", UNSET))

        _labels = d.pop("labels", UNSET)
        labels: Union[Unset, EdgeLabels]
        if isinstance(_labels, Unset):
            labels = UNSET
        else:
            labels = EdgeLabels.from_dict(_labels)

        _metadata = d.pop("metadata", UNSET)
        metadata: Union[Unset, EdgeApprovalMetadata]
        if isinstance(_metadata, Unset):
            metadata = UNSET
        else:
            metadata = EdgeApprovalMetadata.from_dict(_metadata)

        edge_approval = cls(
            approval_ref=approval_ref,
            tenant_id=tenant_id,
            session_id=session_id,
            execution_id=execution_id,
            event_id=event_id,
            principal_id=principal_id,
            requester=requester,
            status=status,
            reason=reason,
            rule_id=rule_id,
            policy_snapshot=policy_snapshot,
            action_hash=action_hash,
            input_hash=input_hash,
            created_at=created_at,
            expires_at=expires_at,
            resolver_id=resolver_id,
            resolved_by=resolved_by,
            decision=decision,
            resolution_reason=resolution_reason,
            resolved_at=resolved_at,
            consumed_at=consumed_at,
            labels=labels,
            metadata=metadata,
        )

        edge_approval.additional_properties = d
        return edge_approval

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
