from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast
from typing import Dict
from typing import Union

if TYPE_CHECKING:
    from ..models.get_approval_context_response_200_policy_snapshot_summary_matched_rule import (
        GetApprovalContextResponse200PolicySnapshotSummaryMatchedRule,
    )


T = TypeVar("T", bound="GetApprovalContextResponse200PolicySnapshotSummary")


@_attrs_define
class GetApprovalContextResponse200PolicySnapshotSummary:
    """
    Attributes:
        rule_count (Union[Unset, int]):
        matched_rule (Union[Unset, GetApprovalContextResponse200PolicySnapshotSummaryMatchedRule]):
        policy_version (Union[Unset, str]):
    """

    rule_count: Union[Unset, int] = UNSET
    matched_rule: Union[Unset, "GetApprovalContextResponse200PolicySnapshotSummaryMatchedRule"] = (
        UNSET
    )
    policy_version: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.get_approval_context_response_200_policy_snapshot_summary_matched_rule import (
            GetApprovalContextResponse200PolicySnapshotSummaryMatchedRule,
        )

        rule_count = self.rule_count

        matched_rule: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.matched_rule, Unset):
            matched_rule = self.matched_rule.to_dict()

        policy_version = self.policy_version

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if rule_count is not UNSET:
            field_dict["rule_count"] = rule_count
        if matched_rule is not UNSET:
            field_dict["matched_rule"] = matched_rule
        if policy_version is not UNSET:
            field_dict["policy_version"] = policy_version

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.get_approval_context_response_200_policy_snapshot_summary_matched_rule import (
            GetApprovalContextResponse200PolicySnapshotSummaryMatchedRule,
        )

        d = src_dict.copy()
        rule_count = d.pop("rule_count", UNSET)

        _matched_rule = d.pop("matched_rule", UNSET)
        matched_rule: Union[Unset, GetApprovalContextResponse200PolicySnapshotSummaryMatchedRule]
        if isinstance(_matched_rule, Unset):
            matched_rule = UNSET
        else:
            matched_rule = GetApprovalContextResponse200PolicySnapshotSummaryMatchedRule.from_dict(
                _matched_rule
            )

        policy_version = d.pop("policy_version", UNSET)

        get_approval_context_response_200_policy_snapshot_summary = cls(
            rule_count=rule_count,
            matched_rule=matched_rule,
            policy_version=policy_version,
        )

        get_approval_context_response_200_policy_snapshot_summary.additional_properties = d
        return get_approval_context_response_200_policy_snapshot_summary

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
