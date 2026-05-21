from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import Union


T = TypeVar("T", bound="GetApprovalContextResponse200PolicySnapshotSummaryMatchedRule")


@_attrs_define
class GetApprovalContextResponse200PolicySnapshotSummaryMatchedRule:
    """
    Attributes:
        id (Union[Unset, str]):
        description (Union[Unset, str]):
        decision (Union[Unset, str]):
        constraints_summary (Union[Unset, str]):
    """

    id: Union[Unset, str] = UNSET
    description: Union[Unset, str] = UNSET
    decision: Union[Unset, str] = UNSET
    constraints_summary: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        id = self.id

        description = self.description

        decision = self.decision

        constraints_summary = self.constraints_summary

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if id is not UNSET:
            field_dict["id"] = id
        if description is not UNSET:
            field_dict["description"] = description
        if decision is not UNSET:
            field_dict["decision"] = decision
        if constraints_summary is not UNSET:
            field_dict["constraints_summary"] = constraints_summary

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        id = d.pop("id", UNSET)

        description = d.pop("description", UNSET)

        decision = d.pop("decision", UNSET)

        constraints_summary = d.pop("constraints_summary", UNSET)

        get_approval_context_response_200_policy_snapshot_summary_matched_rule = cls(
            id=id,
            description=description,
            decision=decision,
            constraints_summary=constraints_summary,
        )

        get_approval_context_response_200_policy_snapshot_summary_matched_rule.additional_properties = d
        return get_approval_context_response_200_policy_snapshot_summary_matched_rule

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
