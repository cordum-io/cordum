from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import Union


T = TypeVar("T", bound="ShadowResultsSummary")


@_attrs_define
class ShadowResultsSummary:
    """
    Attributes:
        bundle_id (str):
        from_ms (int):
        to_ms (int):
        total_evaluated (int):
        escalated_count (int): Shadow would have been stricter than active (e.g. DENY vs ALLOW).
        relaxed_count (int): Shadow would have been more permissive than active.
        approval_differ_count (int): Both reached a terminal decision but differed on REQUIRE_APPROVAL.
        unchanged_count (int):
        truncated_at_max (bool): True when the scan hit its event budget before reaching `to_ms`.
        shadow_bundle_id (Union[Unset, str]):
    """

    bundle_id: str
    from_ms: int
    to_ms: int
    total_evaluated: int
    escalated_count: int
    relaxed_count: int
    approval_differ_count: int
    unchanged_count: int
    truncated_at_max: bool
    shadow_bundle_id: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        bundle_id = self.bundle_id

        from_ms = self.from_ms

        to_ms = self.to_ms

        total_evaluated = self.total_evaluated

        escalated_count = self.escalated_count

        relaxed_count = self.relaxed_count

        approval_differ_count = self.approval_differ_count

        unchanged_count = self.unchanged_count

        truncated_at_max = self.truncated_at_max

        shadow_bundle_id = self.shadow_bundle_id

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "bundle_id": bundle_id,
                "from_ms": from_ms,
                "to_ms": to_ms,
                "total_evaluated": total_evaluated,
                "escalated_count": escalated_count,
                "relaxed_count": relaxed_count,
                "approval_differ_count": approval_differ_count,
                "unchanged_count": unchanged_count,
                "truncated_at_max": truncated_at_max,
            }
        )
        if shadow_bundle_id is not UNSET:
            field_dict["shadow_bundle_id"] = shadow_bundle_id

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        bundle_id = d.pop("bundle_id")

        from_ms = d.pop("from_ms")

        to_ms = d.pop("to_ms")

        total_evaluated = d.pop("total_evaluated")

        escalated_count = d.pop("escalated_count")

        relaxed_count = d.pop("relaxed_count")

        approval_differ_count = d.pop("approval_differ_count")

        unchanged_count = d.pop("unchanged_count")

        truncated_at_max = d.pop("truncated_at_max")

        shadow_bundle_id = d.pop("shadow_bundle_id", UNSET)

        shadow_results_summary = cls(
            bundle_id=bundle_id,
            from_ms=from_ms,
            to_ms=to_ms,
            total_evaluated=total_evaluated,
            escalated_count=escalated_count,
            relaxed_count=relaxed_count,
            approval_differ_count=approval_differ_count,
            unchanged_count=unchanged_count,
            truncated_at_max=truncated_at_max,
            shadow_bundle_id=shadow_bundle_id,
        )

        shadow_results_summary.additional_properties = d
        return shadow_results_summary

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
