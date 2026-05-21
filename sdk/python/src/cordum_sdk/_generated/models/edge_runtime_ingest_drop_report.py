from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.edge_runtime_ingest_drop_report_reason import EdgeRuntimeIngestDropReportReason
from ..types import UNSET, Unset
from typing import Union


T = TypeVar("T", bound="EdgeRuntimeIngestDropReport")


@_attrs_define
class EdgeRuntimeIngestDropReport:
    """
    Attributes:
        source_event_id (Union[Unset, str]):
        kind (Union[Unset, str]):
        reason (Union[Unset, EdgeRuntimeIngestDropReportReason]):
    """

    source_event_id: Union[Unset, str] = UNSET
    kind: Union[Unset, str] = UNSET
    reason: Union[Unset, EdgeRuntimeIngestDropReportReason] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        source_event_id = self.source_event_id

        kind = self.kind

        reason: Union[Unset, str] = UNSET
        if not isinstance(self.reason, Unset):
            reason = self.reason.value

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if source_event_id is not UNSET:
            field_dict["source_event_id"] = source_event_id
        if kind is not UNSET:
            field_dict["kind"] = kind
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        source_event_id = d.pop("source_event_id", UNSET)

        kind = d.pop("kind", UNSET)

        _reason = d.pop("reason", UNSET)
        reason: Union[Unset, EdgeRuntimeIngestDropReportReason]
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = EdgeRuntimeIngestDropReportReason(_reason)

        edge_runtime_ingest_drop_report = cls(
            source_event_id=source_event_id,
            kind=kind,
            reason=reason,
        )

        edge_runtime_ingest_drop_report.additional_properties = d
        return edge_runtime_ingest_drop_report

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
