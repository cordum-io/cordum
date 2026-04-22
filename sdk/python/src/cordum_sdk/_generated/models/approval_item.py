from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.approval_item_decision import ApprovalItemDecision
from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import cast, Union
from typing import Dict
from typing import Union
import datetime

if TYPE_CHECKING:
  from ..models.approval_item_constraints_type_0 import ApprovalItemConstraintsType0
  from ..models.job_summary import JobSummary





T = TypeVar("T", bound="ApprovalItem")


@_attrs_define
class ApprovalItem:
    """ 
        Attributes:
            job (Union[Unset, JobSummary]):
            decision (Union[Unset, ApprovalItemDecision]):
            policy_snapshot (Union[None, Unset, str]):
            policy_rule_id (Union[None, Unset, str]):
            policy_reason (Union[None, Unset, str]):
            constraints (Union['ApprovalItemConstraintsType0', None, Unset]):
            approval_required (Union[Unset, bool]):
            requested_at (Union[Unset, datetime.datetime]):
            decided_at (Union[None, Unset, datetime.datetime]):
            decided_by (Union[None, Unset, str]):
     """

    job: Union[Unset, 'JobSummary'] = UNSET
    decision: Union[Unset, ApprovalItemDecision] = UNSET
    policy_snapshot: Union[None, Unset, str] = UNSET
    policy_rule_id: Union[None, Unset, str] = UNSET
    policy_reason: Union[None, Unset, str] = UNSET
    constraints: Union['ApprovalItemConstraintsType0', None, Unset] = UNSET
    approval_required: Union[Unset, bool] = UNSET
    requested_at: Union[Unset, datetime.datetime] = UNSET
    decided_at: Union[None, Unset, datetime.datetime] = UNSET
    decided_by: Union[None, Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)


    def to_dict(self) -> Dict[str, Any]:
        from ..models.approval_item_constraints_type_0 import ApprovalItemConstraintsType0
        from ..models.job_summary import JobSummary
        job: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.job, Unset):
            job = self.job.to_dict()

        decision: Union[Unset, str] = UNSET
        if not isinstance(self.decision, Unset):
            decision = self.decision.value


        policy_snapshot: Union[None, Unset, str]
        if isinstance(self.policy_snapshot, Unset):
            policy_snapshot = UNSET
        else:
            policy_snapshot = self.policy_snapshot

        policy_rule_id: Union[None, Unset, str]
        if isinstance(self.policy_rule_id, Unset):
            policy_rule_id = UNSET
        else:
            policy_rule_id = self.policy_rule_id

        policy_reason: Union[None, Unset, str]
        if isinstance(self.policy_reason, Unset):
            policy_reason = UNSET
        else:
            policy_reason = self.policy_reason

        constraints: Union[Dict[str, Any], None, Unset]
        if isinstance(self.constraints, Unset):
            constraints = UNSET
        elif isinstance(self.constraints, ApprovalItemConstraintsType0):
            constraints = self.constraints.to_dict()
        else:
            constraints = self.constraints

        approval_required = self.approval_required

        requested_at: Union[Unset, str] = UNSET
        if not isinstance(self.requested_at, Unset):
            requested_at = self.requested_at.isoformat()

        decided_at: Union[None, Unset, str]
        if isinstance(self.decided_at, Unset):
            decided_at = UNSET
        elif isinstance(self.decided_at, datetime.datetime):
            decided_at = self.decided_at.isoformat()
        else:
            decided_at = self.decided_at

        decided_by: Union[None, Unset, str]
        if isinstance(self.decided_by, Unset):
            decided_by = UNSET
        else:
            decided_by = self.decided_by


        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
        })
        if job is not UNSET:
            field_dict["job"] = job
        if decision is not UNSET:
            field_dict["decision"] = decision
        if policy_snapshot is not UNSET:
            field_dict["policy_snapshot"] = policy_snapshot
        if policy_rule_id is not UNSET:
            field_dict["policy_rule_id"] = policy_rule_id
        if policy_reason is not UNSET:
            field_dict["policy_reason"] = policy_reason
        if constraints is not UNSET:
            field_dict["constraints"] = constraints
        if approval_required is not UNSET:
            field_dict["approval_required"] = approval_required
        if requested_at is not UNSET:
            field_dict["requested_at"] = requested_at
        if decided_at is not UNSET:
            field_dict["decided_at"] = decided_at
        if decided_by is not UNSET:
            field_dict["decided_by"] = decided_by

        return field_dict



    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.approval_item_constraints_type_0 import ApprovalItemConstraintsType0
        from ..models.job_summary import JobSummary
        d = src_dict.copy()
        _job = d.pop("job", UNSET)
        job: Union[Unset, JobSummary]
        if isinstance(_job,  Unset):
            job = UNSET
        else:
            job = JobSummary.from_dict(_job)




        _decision = d.pop("decision", UNSET)
        decision: Union[Unset, ApprovalItemDecision]
        if isinstance(_decision,  Unset):
            decision = UNSET
        else:
            decision = ApprovalItemDecision(_decision)




        def _parse_policy_snapshot(data: object) -> Union[None, Unset, str]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, str], data)

        policy_snapshot = _parse_policy_snapshot(d.pop("policy_snapshot", UNSET))


        def _parse_policy_rule_id(data: object) -> Union[None, Unset, str]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, str], data)

        policy_rule_id = _parse_policy_rule_id(d.pop("policy_rule_id", UNSET))


        def _parse_policy_reason(data: object) -> Union[None, Unset, str]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, str], data)

        policy_reason = _parse_policy_reason(d.pop("policy_reason", UNSET))


        def _parse_constraints(data: object) -> Union['ApprovalItemConstraintsType0', None, Unset]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                constraints_type_0 = ApprovalItemConstraintsType0.from_dict(data)



                return constraints_type_0
            except: # noqa: E722
                pass
            return cast(Union['ApprovalItemConstraintsType0', None, Unset], data)

        constraints = _parse_constraints(d.pop("constraints", UNSET))


        approval_required = d.pop("approval_required", UNSET)

        _requested_at = d.pop("requested_at", UNSET)
        requested_at: Union[Unset, datetime.datetime]
        if isinstance(_requested_at,  Unset):
            requested_at = UNSET
        else:
            requested_at = isoparse(_requested_at)




        def _parse_decided_at(data: object) -> Union[None, Unset, datetime.datetime]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                decided_at_type_0 = isoparse(data)



                return decided_at_type_0
            except: # noqa: E722
                pass
            return cast(Union[None, Unset, datetime.datetime], data)

        decided_at = _parse_decided_at(d.pop("decided_at", UNSET))


        def _parse_decided_by(data: object) -> Union[None, Unset, str]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, str], data)

        decided_by = _parse_decided_by(d.pop("decided_by", UNSET))


        approval_item = cls(
            job=job,
            decision=decision,
            policy_snapshot=policy_snapshot,
            policy_rule_id=policy_rule_id,
            policy_reason=policy_reason,
            constraints=constraints,
            approval_required=approval_required,
            requested_at=requested_at,
            decided_at=decided_at,
            decided_by=decided_by,
        )


        approval_item.additional_properties = d
        return approval_item

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
