from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast
from typing import cast, List
from typing import cast, Union
from typing import Dict
from typing import Union

if TYPE_CHECKING:
    from ..models.get_approval_context_response_200_policy_snapshot_summary import (
        GetApprovalContextResponse200PolicySnapshotSummary,
    )
    from ..models.get_approval_context_response_200_prior_approvals_item import (
        GetApprovalContextResponse200PriorApprovalsItem,
    )
    from ..models.get_approval_context_response_200_constraints_type_0 import (
        GetApprovalContextResponse200ConstraintsType0,
    )
    from ..models.get_approval_context_response_200_approval import (
        GetApprovalContextResponse200Approval,
    )
    from ..models.get_approval_context_response_200_blast_radius import (
        GetApprovalContextResponse200BlastRadius,
    )


T = TypeVar("T", bound="GetApprovalContextResponse200")


@_attrs_define
class GetApprovalContextResponse200:
    """
    Attributes:
        approval (Union[Unset, GetApprovalContextResponse200Approval]): Full approval record with decision summary and
            workflow metadata
        blast_radius (Union[Unset, GetApprovalContextResponse200BlastRadius]):
        prior_approvals (Union[Unset, List['GetApprovalContextResponse200PriorApprovalsItem']]):
        rollback_hint (Union[Unset, str]): Pack-provided rollback instructions
        policy_snapshot_summary (Union[Unset, GetApprovalContextResponse200PolicySnapshotSummary]):
        time_remaining_ms (Union[None, Unset, int]): Milliseconds until approval deadline, null if no deadline
        constraints (Union['GetApprovalContextResponse200ConstraintsType0', None, Unset]): Parsed policy constraints
            from safety decision
    """

    approval: Union[Unset, "GetApprovalContextResponse200Approval"] = UNSET
    blast_radius: Union[Unset, "GetApprovalContextResponse200BlastRadius"] = UNSET
    prior_approvals: Union[Unset, List["GetApprovalContextResponse200PriorApprovalsItem"]] = UNSET
    rollback_hint: Union[Unset, str] = UNSET
    policy_snapshot_summary: Union[Unset, "GetApprovalContextResponse200PolicySnapshotSummary"] = (
        UNSET
    )
    time_remaining_ms: Union[None, Unset, int] = UNSET
    constraints: Union["GetApprovalContextResponse200ConstraintsType0", None, Unset] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.get_approval_context_response_200_policy_snapshot_summary import (
            GetApprovalContextResponse200PolicySnapshotSummary,
        )
        from ..models.get_approval_context_response_200_prior_approvals_item import (
            GetApprovalContextResponse200PriorApprovalsItem,
        )
        from ..models.get_approval_context_response_200_constraints_type_0 import (
            GetApprovalContextResponse200ConstraintsType0,
        )
        from ..models.get_approval_context_response_200_approval import (
            GetApprovalContextResponse200Approval,
        )
        from ..models.get_approval_context_response_200_blast_radius import (
            GetApprovalContextResponse200BlastRadius,
        )

        approval: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.approval, Unset):
            approval = self.approval.to_dict()

        blast_radius: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.blast_radius, Unset):
            blast_radius = self.blast_radius.to_dict()

        prior_approvals: Union[Unset, List[Dict[str, Any]]] = UNSET
        if not isinstance(self.prior_approvals, Unset):
            prior_approvals = []
            for prior_approvals_item_data in self.prior_approvals:
                prior_approvals_item = prior_approvals_item_data.to_dict()
                prior_approvals.append(prior_approvals_item)

        rollback_hint = self.rollback_hint

        policy_snapshot_summary: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.policy_snapshot_summary, Unset):
            policy_snapshot_summary = self.policy_snapshot_summary.to_dict()

        time_remaining_ms: Union[None, Unset, int]
        if isinstance(self.time_remaining_ms, Unset):
            time_remaining_ms = UNSET
        else:
            time_remaining_ms = self.time_remaining_ms

        constraints: Union[Dict[str, Any], None, Unset]
        if isinstance(self.constraints, Unset):
            constraints = UNSET
        elif isinstance(self.constraints, GetApprovalContextResponse200ConstraintsType0):
            constraints = self.constraints.to_dict()
        else:
            constraints = self.constraints

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if approval is not UNSET:
            field_dict["approval"] = approval
        if blast_radius is not UNSET:
            field_dict["blast_radius"] = blast_radius
        if prior_approvals is not UNSET:
            field_dict["prior_approvals"] = prior_approvals
        if rollback_hint is not UNSET:
            field_dict["rollback_hint"] = rollback_hint
        if policy_snapshot_summary is not UNSET:
            field_dict["policy_snapshot_summary"] = policy_snapshot_summary
        if time_remaining_ms is not UNSET:
            field_dict["time_remaining_ms"] = time_remaining_ms
        if constraints is not UNSET:
            field_dict["constraints"] = constraints

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.get_approval_context_response_200_policy_snapshot_summary import (
            GetApprovalContextResponse200PolicySnapshotSummary,
        )
        from ..models.get_approval_context_response_200_prior_approvals_item import (
            GetApprovalContextResponse200PriorApprovalsItem,
        )
        from ..models.get_approval_context_response_200_constraints_type_0 import (
            GetApprovalContextResponse200ConstraintsType0,
        )
        from ..models.get_approval_context_response_200_approval import (
            GetApprovalContextResponse200Approval,
        )
        from ..models.get_approval_context_response_200_blast_radius import (
            GetApprovalContextResponse200BlastRadius,
        )

        d = src_dict.copy()
        _approval = d.pop("approval", UNSET)
        approval: Union[Unset, GetApprovalContextResponse200Approval]
        if isinstance(_approval, Unset):
            approval = UNSET
        else:
            approval = GetApprovalContextResponse200Approval.from_dict(_approval)

        _blast_radius = d.pop("blast_radius", UNSET)
        blast_radius: Union[Unset, GetApprovalContextResponse200BlastRadius]
        if isinstance(_blast_radius, Unset):
            blast_radius = UNSET
        else:
            blast_radius = GetApprovalContextResponse200BlastRadius.from_dict(_blast_radius)

        prior_approvals = []
        _prior_approvals = d.pop("prior_approvals", UNSET)
        for prior_approvals_item_data in _prior_approvals or []:
            prior_approvals_item = GetApprovalContextResponse200PriorApprovalsItem.from_dict(
                prior_approvals_item_data
            )

            prior_approvals.append(prior_approvals_item)

        rollback_hint = d.pop("rollback_hint", UNSET)

        _policy_snapshot_summary = d.pop("policy_snapshot_summary", UNSET)
        policy_snapshot_summary: Union[Unset, GetApprovalContextResponse200PolicySnapshotSummary]
        if isinstance(_policy_snapshot_summary, Unset):
            policy_snapshot_summary = UNSET
        else:
            policy_snapshot_summary = GetApprovalContextResponse200PolicySnapshotSummary.from_dict(
                _policy_snapshot_summary
            )

        def _parse_time_remaining_ms(data: object) -> Union[None, Unset, int]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, int], data)

        time_remaining_ms = _parse_time_remaining_ms(d.pop("time_remaining_ms", UNSET))

        def _parse_constraints(
            data: object,
        ) -> Union["GetApprovalContextResponse200ConstraintsType0", None, Unset]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                constraints_type_0 = GetApprovalContextResponse200ConstraintsType0.from_dict(data)

                return constraints_type_0
            except:  # noqa: E722
                pass
            return cast(Union["GetApprovalContextResponse200ConstraintsType0", None, Unset], data)

        constraints = _parse_constraints(d.pop("constraints", UNSET))

        get_approval_context_response_200 = cls(
            approval=approval,
            blast_radius=blast_radius,
            prior_approvals=prior_approvals,
            rollback_hint=rollback_hint,
            policy_snapshot_summary=policy_snapshot_summary,
            time_remaining_ms=time_remaining_ms,
            constraints=constraints,
        )

        get_approval_context_response_200.additional_properties = d
        return get_approval_context_response_200

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
