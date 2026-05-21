from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.auth_source import AuthSource
from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import cast, List
from typing import cast, Union
from typing import Dict
from typing import Union
import datetime

if TYPE_CHECKING:
    from ..models.policy_audit_entry_extra_type_0 import PolicyAuditEntryExtraType0


T = TypeVar("T", bound="PolicyAuditEntry")


@_attrs_define
class PolicyAuditEntry:
    """One entry in the policy audit log. Mirrors `policybundles.PolicyAuditEntry`
    in `core/controlplane/gateway/policybundles/types.go`. The legacy
    `author` / `timestamp` / `bundle_id` (singular) / `snapshot_id`
    fields are preserved for backwards-compat with consumers of the
    v1 spec but are not populated by the current backend handler;
    prefer `actor_id` / `created_at` / `bundle_ids` (plural) /
    `snapshot_before` + `snapshot_after`.

        Attributes:
            id (str):
            action (str):
            created_at (str): RFC3339 timestamp when the audit entry was created. Plain
                string (not `format: date-time`) because the backend stores
                the raw value and lex-compares it against `after` / `before`
                query params.
            author (Union[Unset, str]): Deprecated. The current backend handler does not populate this
                field; it remains in the schema only to avoid breaking
                consumers that read the v1 type.
            timestamp (Union[Unset, datetime.datetime]): Deprecated. Use `created_at`. The current backend handler
                does not populate this field.
            bundle_id (Union[None, Unset, str]): Deprecated. Use `bundle_ids` (plural). The current backend
                handler does not populate this field.
            snapshot_id (Union[None, Unset, str]): Deprecated. Use `snapshot_before` and `snapshot_after`. The
                current backend handler does not populate this field.
            message (Union[None, Unset, str]):
            resource_type (Union[None, Unset, str]): Audited resource kind, e.g. `rule`, `bundle`, `input`, `output`.
            resource_id (Union[None, Unset, str]):
            resource_name (Union[None, Unset, str]):
            actor_id (Union[None, Unset, str]): Identifier of the principal who performed the action.
            role (Union[None, Unset, str]):
            auth_source (Union[Unset, AuthSource]): Authentication mechanism that validated the request. Mirrors the
                `auth.AuthSource` typed enum at
                `core/controlplane/gateway/auth/types.go`.
            agent_id (Union[None, Unset, str]):
            agent_name (Union[None, Unset, str]):
            agent_risk_tier (Union[None, Unset, str]):
            bundle_ids (Union[List[str], None, Unset]): Bundles affected by this audit event. Replaces the legacy
                singular `bundle_id` field.
            reason (Union[None, Unset, str]):
            decision (Union[None, Unset, str]):
            matched_rule (Union[None, Unset, str]):
            policy_version (Union[None, Unset, str]):
            extra (Union['PolicyAuditEntryExtraType0', None, Unset]): Free-form per-event metadata. Keys and values are
                emitted
                verbatim from the gateway handler's audit-construction site.
            snapshot_before (Union[None, Unset, str]): Audit-chain hash of the resource state before the action.
            snapshot_after (Union[None, Unset, str]): Audit-chain hash of the resource state after the action.
    """

    id: str
    action: str
    created_at: str
    author: Union[Unset, str] = UNSET
    timestamp: Union[Unset, datetime.datetime] = UNSET
    bundle_id: Union[None, Unset, str] = UNSET
    snapshot_id: Union[None, Unset, str] = UNSET
    message: Union[None, Unset, str] = UNSET
    resource_type: Union[None, Unset, str] = UNSET
    resource_id: Union[None, Unset, str] = UNSET
    resource_name: Union[None, Unset, str] = UNSET
    actor_id: Union[None, Unset, str] = UNSET
    role: Union[None, Unset, str] = UNSET
    auth_source: Union[Unset, AuthSource] = UNSET
    agent_id: Union[None, Unset, str] = UNSET
    agent_name: Union[None, Unset, str] = UNSET
    agent_risk_tier: Union[None, Unset, str] = UNSET
    bundle_ids: Union[List[str], None, Unset] = UNSET
    reason: Union[None, Unset, str] = UNSET
    decision: Union[None, Unset, str] = UNSET
    matched_rule: Union[None, Unset, str] = UNSET
    policy_version: Union[None, Unset, str] = UNSET
    extra: Union["PolicyAuditEntryExtraType0", None, Unset] = UNSET
    snapshot_before: Union[None, Unset, str] = UNSET
    snapshot_after: Union[None, Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.policy_audit_entry_extra_type_0 import PolicyAuditEntryExtraType0

        id = self.id

        action = self.action

        created_at = self.created_at

        author = self.author

        timestamp: Union[Unset, str] = UNSET
        if not isinstance(self.timestamp, Unset):
            timestamp = self.timestamp.isoformat()

        bundle_id: Union[None, Unset, str]
        if isinstance(self.bundle_id, Unset):
            bundle_id = UNSET
        else:
            bundle_id = self.bundle_id

        snapshot_id: Union[None, Unset, str]
        if isinstance(self.snapshot_id, Unset):
            snapshot_id = UNSET
        else:
            snapshot_id = self.snapshot_id

        message: Union[None, Unset, str]
        if isinstance(self.message, Unset):
            message = UNSET
        else:
            message = self.message

        resource_type: Union[None, Unset, str]
        if isinstance(self.resource_type, Unset):
            resource_type = UNSET
        else:
            resource_type = self.resource_type

        resource_id: Union[None, Unset, str]
        if isinstance(self.resource_id, Unset):
            resource_id = UNSET
        else:
            resource_id = self.resource_id

        resource_name: Union[None, Unset, str]
        if isinstance(self.resource_name, Unset):
            resource_name = UNSET
        else:
            resource_name = self.resource_name

        actor_id: Union[None, Unset, str]
        if isinstance(self.actor_id, Unset):
            actor_id = UNSET
        else:
            actor_id = self.actor_id

        role: Union[None, Unset, str]
        if isinstance(self.role, Unset):
            role = UNSET
        else:
            role = self.role

        auth_source: Union[Unset, str] = UNSET
        if not isinstance(self.auth_source, Unset):
            auth_source = self.auth_source.value

        agent_id: Union[None, Unset, str]
        if isinstance(self.agent_id, Unset):
            agent_id = UNSET
        else:
            agent_id = self.agent_id

        agent_name: Union[None, Unset, str]
        if isinstance(self.agent_name, Unset):
            agent_name = UNSET
        else:
            agent_name = self.agent_name

        agent_risk_tier: Union[None, Unset, str]
        if isinstance(self.agent_risk_tier, Unset):
            agent_risk_tier = UNSET
        else:
            agent_risk_tier = self.agent_risk_tier

        bundle_ids: Union[List[str], None, Unset]
        if isinstance(self.bundle_ids, Unset):
            bundle_ids = UNSET
        elif isinstance(self.bundle_ids, list):
            bundle_ids = self.bundle_ids

        else:
            bundle_ids = self.bundle_ids

        reason: Union[None, Unset, str]
        if isinstance(self.reason, Unset):
            reason = UNSET
        else:
            reason = self.reason

        decision: Union[None, Unset, str]
        if isinstance(self.decision, Unset):
            decision = UNSET
        else:
            decision = self.decision

        matched_rule: Union[None, Unset, str]
        if isinstance(self.matched_rule, Unset):
            matched_rule = UNSET
        else:
            matched_rule = self.matched_rule

        policy_version: Union[None, Unset, str]
        if isinstance(self.policy_version, Unset):
            policy_version = UNSET
        else:
            policy_version = self.policy_version

        extra: Union[Dict[str, Any], None, Unset]
        if isinstance(self.extra, Unset):
            extra = UNSET
        elif isinstance(self.extra, PolicyAuditEntryExtraType0):
            extra = self.extra.to_dict()
        else:
            extra = self.extra

        snapshot_before: Union[None, Unset, str]
        if isinstance(self.snapshot_before, Unset):
            snapshot_before = UNSET
        else:
            snapshot_before = self.snapshot_before

        snapshot_after: Union[None, Unset, str]
        if isinstance(self.snapshot_after, Unset):
            snapshot_after = UNSET
        else:
            snapshot_after = self.snapshot_after

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "action": action,
                "created_at": created_at,
            }
        )
        if author is not UNSET:
            field_dict["author"] = author
        if timestamp is not UNSET:
            field_dict["timestamp"] = timestamp
        if bundle_id is not UNSET:
            field_dict["bundle_id"] = bundle_id
        if snapshot_id is not UNSET:
            field_dict["snapshot_id"] = snapshot_id
        if message is not UNSET:
            field_dict["message"] = message
        if resource_type is not UNSET:
            field_dict["resource_type"] = resource_type
        if resource_id is not UNSET:
            field_dict["resource_id"] = resource_id
        if resource_name is not UNSET:
            field_dict["resource_name"] = resource_name
        if actor_id is not UNSET:
            field_dict["actor_id"] = actor_id
        if role is not UNSET:
            field_dict["role"] = role
        if auth_source is not UNSET:
            field_dict["auth_source"] = auth_source
        if agent_id is not UNSET:
            field_dict["agent_id"] = agent_id
        if agent_name is not UNSET:
            field_dict["agent_name"] = agent_name
        if agent_risk_tier is not UNSET:
            field_dict["agent_risk_tier"] = agent_risk_tier
        if bundle_ids is not UNSET:
            field_dict["bundle_ids"] = bundle_ids
        if reason is not UNSET:
            field_dict["reason"] = reason
        if decision is not UNSET:
            field_dict["decision"] = decision
        if matched_rule is not UNSET:
            field_dict["matched_rule"] = matched_rule
        if policy_version is not UNSET:
            field_dict["policy_version"] = policy_version
        if extra is not UNSET:
            field_dict["extra"] = extra
        if snapshot_before is not UNSET:
            field_dict["snapshot_before"] = snapshot_before
        if snapshot_after is not UNSET:
            field_dict["snapshot_after"] = snapshot_after

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.policy_audit_entry_extra_type_0 import PolicyAuditEntryExtraType0

        d = src_dict.copy()
        id = d.pop("id")

        action = d.pop("action")

        created_at = d.pop("created_at")

        author = d.pop("author", UNSET)

        _timestamp = d.pop("timestamp", UNSET)
        timestamp: Union[Unset, datetime.datetime]
        if isinstance(_timestamp, Unset):
            timestamp = UNSET
        else:
            timestamp = isoparse(_timestamp)

        def _parse_bundle_id(data: object) -> Union[None, Unset, str]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, str], data)

        bundle_id = _parse_bundle_id(d.pop("bundle_id", UNSET))

        def _parse_snapshot_id(data: object) -> Union[None, Unset, str]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, str], data)

        snapshot_id = _parse_snapshot_id(d.pop("snapshot_id", UNSET))

        def _parse_message(data: object) -> Union[None, Unset, str]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, str], data)

        message = _parse_message(d.pop("message", UNSET))

        def _parse_resource_type(data: object) -> Union[None, Unset, str]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, str], data)

        resource_type = _parse_resource_type(d.pop("resource_type", UNSET))

        def _parse_resource_id(data: object) -> Union[None, Unset, str]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, str], data)

        resource_id = _parse_resource_id(d.pop("resource_id", UNSET))

        def _parse_resource_name(data: object) -> Union[None, Unset, str]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, str], data)

        resource_name = _parse_resource_name(d.pop("resource_name", UNSET))

        def _parse_actor_id(data: object) -> Union[None, Unset, str]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, str], data)

        actor_id = _parse_actor_id(d.pop("actor_id", UNSET))

        def _parse_role(data: object) -> Union[None, Unset, str]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, str], data)

        role = _parse_role(d.pop("role", UNSET))

        _auth_source = d.pop("auth_source", UNSET)
        auth_source: Union[Unset, AuthSource]
        if isinstance(_auth_source, Unset):
            auth_source = UNSET
        else:
            auth_source = AuthSource(_auth_source)

        def _parse_agent_id(data: object) -> Union[None, Unset, str]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, str], data)

        agent_id = _parse_agent_id(d.pop("agent_id", UNSET))

        def _parse_agent_name(data: object) -> Union[None, Unset, str]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, str], data)

        agent_name = _parse_agent_name(d.pop("agent_name", UNSET))

        def _parse_agent_risk_tier(data: object) -> Union[None, Unset, str]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, str], data)

        agent_risk_tier = _parse_agent_risk_tier(d.pop("agent_risk_tier", UNSET))

        def _parse_bundle_ids(data: object) -> Union[List[str], None, Unset]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, list):
                    raise TypeError()
                bundle_ids_type_0 = cast(List[str], data)

                return bundle_ids_type_0
            except:  # noqa: E722
                pass
            return cast(Union[List[str], None, Unset], data)

        bundle_ids = _parse_bundle_ids(d.pop("bundle_ids", UNSET))

        def _parse_reason(data: object) -> Union[None, Unset, str]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, str], data)

        reason = _parse_reason(d.pop("reason", UNSET))

        def _parse_decision(data: object) -> Union[None, Unset, str]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, str], data)

        decision = _parse_decision(d.pop("decision", UNSET))

        def _parse_matched_rule(data: object) -> Union[None, Unset, str]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, str], data)

        matched_rule = _parse_matched_rule(d.pop("matched_rule", UNSET))

        def _parse_policy_version(data: object) -> Union[None, Unset, str]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, str], data)

        policy_version = _parse_policy_version(d.pop("policy_version", UNSET))

        def _parse_extra(data: object) -> Union["PolicyAuditEntryExtraType0", None, Unset]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                extra_type_0 = PolicyAuditEntryExtraType0.from_dict(data)

                return extra_type_0
            except:  # noqa: E722
                pass
            return cast(Union["PolicyAuditEntryExtraType0", None, Unset], data)

        extra = _parse_extra(d.pop("extra", UNSET))

        def _parse_snapshot_before(data: object) -> Union[None, Unset, str]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, str], data)

        snapshot_before = _parse_snapshot_before(d.pop("snapshot_before", UNSET))

        def _parse_snapshot_after(data: object) -> Union[None, Unset, str]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, str], data)

        snapshot_after = _parse_snapshot_after(d.pop("snapshot_after", UNSET))

        policy_audit_entry = cls(
            id=id,
            action=action,
            created_at=created_at,
            author=author,
            timestamp=timestamp,
            bundle_id=bundle_id,
            snapshot_id=snapshot_id,
            message=message,
            resource_type=resource_type,
            resource_id=resource_id,
            resource_name=resource_name,
            actor_id=actor_id,
            role=role,
            auth_source=auth_source,
            agent_id=agent_id,
            agent_name=agent_name,
            agent_risk_tier=agent_risk_tier,
            bundle_ids=bundle_ids,
            reason=reason,
            decision=decision,
            matched_rule=matched_rule,
            policy_version=policy_version,
            extra=extra,
            snapshot_before=snapshot_before,
            snapshot_after=snapshot_after,
        )

        policy_audit_entry.additional_properties = d
        return policy_audit_entry

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
