from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import Union


T = TypeVar("T", bound="GetApprovalContextResponse200PriorApprovalsItem")


@_attrs_define
class GetApprovalContextResponse200PriorApprovalsItem:
    """
    Attributes:
        job_id (Union[Unset, str]):
        topic (Union[Unset, str]):
        tenant (Union[Unset, str]):
        decision (Union[Unset, str]):
        resolved_by (Union[Unset, str]):
        resolved_at (Union[Unset, int]):
        was_approved (Union[Unset, bool]):
    """

    job_id: Union[Unset, str] = UNSET
    topic: Union[Unset, str] = UNSET
    tenant: Union[Unset, str] = UNSET
    decision: Union[Unset, str] = UNSET
    resolved_by: Union[Unset, str] = UNSET
    resolved_at: Union[Unset, int] = UNSET
    was_approved: Union[Unset, bool] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        job_id = self.job_id

        topic = self.topic

        tenant = self.tenant

        decision = self.decision

        resolved_by = self.resolved_by

        resolved_at = self.resolved_at

        was_approved = self.was_approved

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if job_id is not UNSET:
            field_dict["job_id"] = job_id
        if topic is not UNSET:
            field_dict["topic"] = topic
        if tenant is not UNSET:
            field_dict["tenant"] = tenant
        if decision is not UNSET:
            field_dict["decision"] = decision
        if resolved_by is not UNSET:
            field_dict["resolved_by"] = resolved_by
        if resolved_at is not UNSET:
            field_dict["resolved_at"] = resolved_at
        if was_approved is not UNSET:
            field_dict["was_approved"] = was_approved

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        job_id = d.pop("job_id", UNSET)

        topic = d.pop("topic", UNSET)

        tenant = d.pop("tenant", UNSET)

        decision = d.pop("decision", UNSET)

        resolved_by = d.pop("resolved_by", UNSET)

        resolved_at = d.pop("resolved_at", UNSET)

        was_approved = d.pop("was_approved", UNSET)

        get_approval_context_response_200_prior_approvals_item = cls(
            job_id=job_id,
            topic=topic,
            tenant=tenant,
            decision=decision,
            resolved_by=resolved_by,
            resolved_at=resolved_at,
            was_approved=was_approved,
        )

        get_approval_context_response_200_prior_approvals_item.additional_properties = d
        return get_approval_context_response_200_prior_approvals_item

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
