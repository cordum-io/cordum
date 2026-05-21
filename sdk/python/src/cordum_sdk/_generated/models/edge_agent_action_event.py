from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.edge_agent_action_event_decision import EdgeAgentActionEventDecision
from ..models.edge_agent_action_event_layer import EdgeAgentActionEventLayer
from ..models.edge_agent_action_event_status import EdgeAgentActionEventStatus
from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import cast, List
from typing import cast, Union
from typing import Dict
from typing import Union
import datetime

if TYPE_CHECKING:
    from ..models.edge_agent_action_event_input_redacted_type_0 import (
        EdgeAgentActionEventInputRedactedType0,
    )
    from ..models.edge_labels import EdgeLabels
    from ..models.edge_artifact_pointer import EdgeArtifactPointer


T = TypeVar("T", bound="EdgeAgentActionEvent")


@_attrs_define
class EdgeAgentActionEvent:
    """
    Attributes:
        event_id (str):
        session_id (str):
        execution_id (str):
        tenant_id (str):
        seq (int):
        ts (datetime.datetime):
        layer (EdgeAgentActionEventLayer):
        kind (str): Open-ended non-empty event kind such as `hook.pre_tool_use` or `mcp.tool.pre`.
        decision (EdgeAgentActionEventDecision):
        status (EdgeAgentActionEventStatus):
        principal_id (Union[Unset, str]):
        agent_product (Union[Unset, str]):
        tool_name (Union[Unset, str]):
        tool_use_id (Union[Unset, str]):
        action_name (Union[Unset, str]):
        capability (Union[Unset, str]):
        risk_tags (Union[Unset, List[str]]):
        input_redacted (Union['EdgeAgentActionEventInputRedactedType0', None, Unset]): Bounded redacted input summary
            safe to persist and return.
        input_hash (Union[Unset, str]): Stable `sha256:` hash for the original input when available.
        decision_reason (Union[Unset, str]):
        rule_id (Union[Unset, str]):
        policy_snapshot (Union[Unset, str]):
        approval_ref (Union[Unset, str]):
        artifact_ptrs (Union[Unset, List['EdgeArtifactPointer']]): References to redacted external evidence artifacts.
            Large tool payloads/transcripts are represented here instead of inline raw event fields.
        duration_ms (Union[Unset, int]):
        error_code (Union[Unset, str]):
        error_message (Union[Unset, str]):
        labels (Union[Unset, EdgeLabels]):
    """

    event_id: str
    session_id: str
    execution_id: str
    tenant_id: str
    seq: int
    ts: datetime.datetime
    layer: EdgeAgentActionEventLayer
    kind: str
    decision: EdgeAgentActionEventDecision
    status: EdgeAgentActionEventStatus
    principal_id: Union[Unset, str] = UNSET
    agent_product: Union[Unset, str] = UNSET
    tool_name: Union[Unset, str] = UNSET
    tool_use_id: Union[Unset, str] = UNSET
    action_name: Union[Unset, str] = UNSET
    capability: Union[Unset, str] = UNSET
    risk_tags: Union[Unset, List[str]] = UNSET
    input_redacted: Union["EdgeAgentActionEventInputRedactedType0", None, Unset] = UNSET
    input_hash: Union[Unset, str] = UNSET
    decision_reason: Union[Unset, str] = UNSET
    rule_id: Union[Unset, str] = UNSET
    policy_snapshot: Union[Unset, str] = UNSET
    approval_ref: Union[Unset, str] = UNSET
    artifact_ptrs: Union[Unset, List["EdgeArtifactPointer"]] = UNSET
    duration_ms: Union[Unset, int] = UNSET
    error_code: Union[Unset, str] = UNSET
    error_message: Union[Unset, str] = UNSET
    labels: Union[Unset, "EdgeLabels"] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.edge_agent_action_event_input_redacted_type_0 import (
            EdgeAgentActionEventInputRedactedType0,
        )
        from ..models.edge_labels import EdgeLabels
        from ..models.edge_artifact_pointer import EdgeArtifactPointer

        event_id = self.event_id

        session_id = self.session_id

        execution_id = self.execution_id

        tenant_id = self.tenant_id

        seq = self.seq

        ts = self.ts.isoformat()

        layer = self.layer.value

        kind = self.kind

        decision = self.decision.value

        status = self.status.value

        principal_id = self.principal_id

        agent_product = self.agent_product

        tool_name = self.tool_name

        tool_use_id = self.tool_use_id

        action_name = self.action_name

        capability = self.capability

        risk_tags: Union[Unset, List[str]] = UNSET
        if not isinstance(self.risk_tags, Unset):
            risk_tags = self.risk_tags

        input_redacted: Union[Dict[str, Any], None, Unset]
        if isinstance(self.input_redacted, Unset):
            input_redacted = UNSET
        elif isinstance(self.input_redacted, EdgeAgentActionEventInputRedactedType0):
            input_redacted = self.input_redacted.to_dict()
        else:
            input_redacted = self.input_redacted

        input_hash = self.input_hash

        decision_reason = self.decision_reason

        rule_id = self.rule_id

        policy_snapshot = self.policy_snapshot

        approval_ref = self.approval_ref

        artifact_ptrs: Union[Unset, List[Dict[str, Any]]] = UNSET
        if not isinstance(self.artifact_ptrs, Unset):
            artifact_ptrs = []
            for artifact_ptrs_item_data in self.artifact_ptrs:
                artifact_ptrs_item = artifact_ptrs_item_data.to_dict()
                artifact_ptrs.append(artifact_ptrs_item)

        duration_ms = self.duration_ms

        error_code = self.error_code

        error_message = self.error_message

        labels: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.labels, Unset):
            labels = self.labels.to_dict()

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "event_id": event_id,
                "session_id": session_id,
                "execution_id": execution_id,
                "tenant_id": tenant_id,
                "seq": seq,
                "ts": ts,
                "layer": layer,
                "kind": kind,
                "decision": decision,
                "status": status,
            }
        )
        if principal_id is not UNSET:
            field_dict["principal_id"] = principal_id
        if agent_product is not UNSET:
            field_dict["agent_product"] = agent_product
        if tool_name is not UNSET:
            field_dict["tool_name"] = tool_name
        if tool_use_id is not UNSET:
            field_dict["tool_use_id"] = tool_use_id
        if action_name is not UNSET:
            field_dict["action_name"] = action_name
        if capability is not UNSET:
            field_dict["capability"] = capability
        if risk_tags is not UNSET:
            field_dict["risk_tags"] = risk_tags
        if input_redacted is not UNSET:
            field_dict["input_redacted"] = input_redacted
        if input_hash is not UNSET:
            field_dict["input_hash"] = input_hash
        if decision_reason is not UNSET:
            field_dict["decision_reason"] = decision_reason
        if rule_id is not UNSET:
            field_dict["rule_id"] = rule_id
        if policy_snapshot is not UNSET:
            field_dict["policy_snapshot"] = policy_snapshot
        if approval_ref is not UNSET:
            field_dict["approval_ref"] = approval_ref
        if artifact_ptrs is not UNSET:
            field_dict["artifact_ptrs"] = artifact_ptrs
        if duration_ms is not UNSET:
            field_dict["duration_ms"] = duration_ms
        if error_code is not UNSET:
            field_dict["error_code"] = error_code
        if error_message is not UNSET:
            field_dict["error_message"] = error_message
        if labels is not UNSET:
            field_dict["labels"] = labels

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.edge_agent_action_event_input_redacted_type_0 import (
            EdgeAgentActionEventInputRedactedType0,
        )
        from ..models.edge_labels import EdgeLabels
        from ..models.edge_artifact_pointer import EdgeArtifactPointer

        d = src_dict.copy()
        event_id = d.pop("event_id")

        session_id = d.pop("session_id")

        execution_id = d.pop("execution_id")

        tenant_id = d.pop("tenant_id")

        seq = d.pop("seq")

        ts = isoparse(d.pop("ts"))

        layer = EdgeAgentActionEventLayer(d.pop("layer"))

        kind = d.pop("kind")

        decision = EdgeAgentActionEventDecision(d.pop("decision"))

        status = EdgeAgentActionEventStatus(d.pop("status"))

        principal_id = d.pop("principal_id", UNSET)

        agent_product = d.pop("agent_product", UNSET)

        tool_name = d.pop("tool_name", UNSET)

        tool_use_id = d.pop("tool_use_id", UNSET)

        action_name = d.pop("action_name", UNSET)

        capability = d.pop("capability", UNSET)

        risk_tags = cast(List[str], d.pop("risk_tags", UNSET))

        def _parse_input_redacted(
            data: object,
        ) -> Union["EdgeAgentActionEventInputRedactedType0", None, Unset]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                input_redacted_type_0 = EdgeAgentActionEventInputRedactedType0.from_dict(data)

                return input_redacted_type_0
            except:  # noqa: E722
                pass
            return cast(Union["EdgeAgentActionEventInputRedactedType0", None, Unset], data)

        input_redacted = _parse_input_redacted(d.pop("input_redacted", UNSET))

        input_hash = d.pop("input_hash", UNSET)

        decision_reason = d.pop("decision_reason", UNSET)

        rule_id = d.pop("rule_id", UNSET)

        policy_snapshot = d.pop("policy_snapshot", UNSET)

        approval_ref = d.pop("approval_ref", UNSET)

        artifact_ptrs = []
        _artifact_ptrs = d.pop("artifact_ptrs", UNSET)
        for artifact_ptrs_item_data in _artifact_ptrs or []:
            artifact_ptrs_item = EdgeArtifactPointer.from_dict(artifact_ptrs_item_data)

            artifact_ptrs.append(artifact_ptrs_item)

        duration_ms = d.pop("duration_ms", UNSET)

        error_code = d.pop("error_code", UNSET)

        error_message = d.pop("error_message", UNSET)

        _labels = d.pop("labels", UNSET)
        labels: Union[Unset, EdgeLabels]
        if isinstance(_labels, Unset):
            labels = UNSET
        else:
            labels = EdgeLabels.from_dict(_labels)

        edge_agent_action_event = cls(
            event_id=event_id,
            session_id=session_id,
            execution_id=execution_id,
            tenant_id=tenant_id,
            seq=seq,
            ts=ts,
            layer=layer,
            kind=kind,
            decision=decision,
            status=status,
            principal_id=principal_id,
            agent_product=agent_product,
            tool_name=tool_name,
            tool_use_id=tool_use_id,
            action_name=action_name,
            capability=capability,
            risk_tags=risk_tags,
            input_redacted=input_redacted,
            input_hash=input_hash,
            decision_reason=decision_reason,
            rule_id=rule_id,
            policy_snapshot=policy_snapshot,
            approval_ref=approval_ref,
            artifact_ptrs=artifact_ptrs,
            duration_ms=duration_ms,
            error_code=error_code,
            error_message=error_message,
            labels=labels,
        )

        edge_agent_action_event.additional_properties = d
        return edge_agent_action_event

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
