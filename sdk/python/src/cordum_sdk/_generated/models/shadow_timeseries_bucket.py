from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset


T = TypeVar("T", bound="ShadowTimeseriesBucket")


@_attrs_define
class ShadowTimeseriesBucket:
    """
    Attributes:
        ts_ms (int): Bucket start time (aligned down to the bucket boundary).
        escalated (int):
        relaxed (int):
        approval_differ (int):
        unchanged (int):
        total (int):
    """

    ts_ms: int
    escalated: int
    relaxed: int
    approval_differ: int
    unchanged: int
    total: int
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        ts_ms = self.ts_ms

        escalated = self.escalated

        relaxed = self.relaxed

        approval_differ = self.approval_differ

        unchanged = self.unchanged

        total = self.total

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "ts_ms": ts_ms,
                "escalated": escalated,
                "relaxed": relaxed,
                "approval_differ": approval_differ,
                "unchanged": unchanged,
                "total": total,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        ts_ms = d.pop("ts_ms")

        escalated = d.pop("escalated")

        relaxed = d.pop("relaxed")

        approval_differ = d.pop("approval_differ")

        unchanged = d.pop("unchanged")

        total = d.pop("total")

        shadow_timeseries_bucket = cls(
            ts_ms=ts_ms,
            escalated=escalated,
            relaxed=relaxed,
            approval_differ=approval_differ,
            unchanged=unchanged,
            total=total,
        )

        shadow_timeseries_bucket.additional_properties = d
        return shadow_timeseries_bucket

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
