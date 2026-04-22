from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast, Union
from typing import Union






T = TypeVar("T", bound="ApprovalAnalyticsSummary")


@_attrs_define
class ApprovalAnalyticsSummary:
    """ 
        Attributes:
            total (int): Total decisions with verdict=require_approval in the window (includes still-pending).
            approved (int):
            rejected (int):
            expired (int):
            auto_resolved (int): Resolutions driven by lifecycle events (expire/invalidate/repair), not human decision.
            manual_resolved (int): Resolutions where a human approver entered approve or reject.
            avg_time_to_approve_seconds (Union[None, Unset, float]): Null when no approvals resolved in the window —
                distinguishes "no data" from "all resolved in 0 s".
            p50 (Union[None, Unset, float]):
            p90 (Union[None, Unset, float]):
            p99 (Union[None, Unset, float]):
     """

    total: int
    approved: int
    rejected: int
    expired: int
    auto_resolved: int
    manual_resolved: int
    avg_time_to_approve_seconds: Union[None, Unset, float] = UNSET
    p50: Union[None, Unset, float] = UNSET
    p90: Union[None, Unset, float] = UNSET
    p99: Union[None, Unset, float] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)


    def to_dict(self) -> Dict[str, Any]:
        total = self.total

        approved = self.approved

        rejected = self.rejected

        expired = self.expired

        auto_resolved = self.auto_resolved

        manual_resolved = self.manual_resolved

        avg_time_to_approve_seconds: Union[None, Unset, float]
        if isinstance(self.avg_time_to_approve_seconds, Unset):
            avg_time_to_approve_seconds = UNSET
        else:
            avg_time_to_approve_seconds = self.avg_time_to_approve_seconds

        p50: Union[None, Unset, float]
        if isinstance(self.p50, Unset):
            p50 = UNSET
        else:
            p50 = self.p50

        p90: Union[None, Unset, float]
        if isinstance(self.p90, Unset):
            p90 = UNSET
        else:
            p90 = self.p90

        p99: Union[None, Unset, float]
        if isinstance(self.p99, Unset):
            p99 = UNSET
        else:
            p99 = self.p99


        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
            "total": total,
            "approved": approved,
            "rejected": rejected,
            "expired": expired,
            "auto_resolved": auto_resolved,
            "manual_resolved": manual_resolved,
        })
        if avg_time_to_approve_seconds is not UNSET:
            field_dict["avg_time_to_approve_seconds"] = avg_time_to_approve_seconds
        if p50 is not UNSET:
            field_dict["p50"] = p50
        if p90 is not UNSET:
            field_dict["p90"] = p90
        if p99 is not UNSET:
            field_dict["p99"] = p99

        return field_dict



    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        total = d.pop("total")

        approved = d.pop("approved")

        rejected = d.pop("rejected")

        expired = d.pop("expired")

        auto_resolved = d.pop("auto_resolved")

        manual_resolved = d.pop("manual_resolved")

        def _parse_avg_time_to_approve_seconds(data: object) -> Union[None, Unset, float]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, float], data)

        avg_time_to_approve_seconds = _parse_avg_time_to_approve_seconds(d.pop("avg_time_to_approve_seconds", UNSET))


        def _parse_p50(data: object) -> Union[None, Unset, float]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, float], data)

        p50 = _parse_p50(d.pop("p50", UNSET))


        def _parse_p90(data: object) -> Union[None, Unset, float]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, float], data)

        p90 = _parse_p90(d.pop("p90", UNSET))


        def _parse_p99(data: object) -> Union[None, Unset, float]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, float], data)

        p99 = _parse_p99(d.pop("p99", UNSET))


        approval_analytics_summary = cls(
            total=total,
            approved=approved,
            rejected=rejected,
            expired=expired,
            auto_resolved=auto_resolved,
            manual_resolved=manual_resolved,
            avg_time_to_approve_seconds=avg_time_to_approve_seconds,
            p50=p50,
            p90=p90,
            p99=p99,
        )


        approval_analytics_summary.additional_properties = d
        return approval_analytics_summary

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
