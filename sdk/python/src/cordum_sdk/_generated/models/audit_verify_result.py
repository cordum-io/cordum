from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.audit_verify_result_status import AuditVerifyResultStatus
from ..types import UNSET, Unset
from typing import cast
from typing import cast, List
from typing import Dict
from typing import Union

if TYPE_CHECKING:
    from ..models.audit_verify_gap import AuditVerifyGap


T = TypeVar("T", bound="AuditVerifyResult")


@_attrs_define
class AuditVerifyResult:
    """
    Attributes:
        status (AuditVerifyResultStatus):
        total_events (int): Number of events walked in the requested range.
        verified_events (int): Number of events whose hash and linkage verified.
        gaps (List['AuditVerifyGap']):
        retention_boundary_seq (int): Lowest sequence number still present in the retained stream.
        retention_window_hours (Union[Unset, float]): Configured audit retention window in hours.
        first_seq (Union[Unset, int]): First sequence number observed in the verified range.
        last_seq (Union[Unset, int]): Last sequence number observed in the verified range.
    """

    status: AuditVerifyResultStatus
    total_events: int
    verified_events: int
    gaps: List["AuditVerifyGap"]
    retention_boundary_seq: int
    retention_window_hours: Union[Unset, float] = UNSET
    first_seq: Union[Unset, int] = UNSET
    last_seq: Union[Unset, int] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.audit_verify_gap import AuditVerifyGap

        status = self.status.value

        total_events = self.total_events

        verified_events = self.verified_events

        gaps = []
        for gaps_item_data in self.gaps:
            gaps_item = gaps_item_data.to_dict()
            gaps.append(gaps_item)

        retention_boundary_seq = self.retention_boundary_seq

        retention_window_hours = self.retention_window_hours

        first_seq = self.first_seq

        last_seq = self.last_seq

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
                "total_events": total_events,
                "verified_events": verified_events,
                "gaps": gaps,
                "retention_boundary_seq": retention_boundary_seq,
            }
        )
        if retention_window_hours is not UNSET:
            field_dict["retention_window_hours"] = retention_window_hours
        if first_seq is not UNSET:
            field_dict["first_seq"] = first_seq
        if last_seq is not UNSET:
            field_dict["last_seq"] = last_seq

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.audit_verify_gap import AuditVerifyGap

        d = src_dict.copy()
        status = AuditVerifyResultStatus(d.pop("status"))

        total_events = d.pop("total_events")

        verified_events = d.pop("verified_events")

        gaps = []
        _gaps = d.pop("gaps")
        for gaps_item_data in _gaps:
            gaps_item = AuditVerifyGap.from_dict(gaps_item_data)

            gaps.append(gaps_item)

        retention_boundary_seq = d.pop("retention_boundary_seq")

        retention_window_hours = d.pop("retention_window_hours", UNSET)

        first_seq = d.pop("first_seq", UNSET)

        last_seq = d.pop("last_seq", UNSET)

        audit_verify_result = cls(
            status=status,
            total_events=total_events,
            verified_events=verified_events,
            gaps=gaps,
            retention_boundary_seq=retention_boundary_seq,
            retention_window_hours=retention_window_hours,
            first_seq=first_seq,
            last_seq=last_seq,
        )

        audit_verify_result.additional_properties = d
        return audit_verify_result

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
