from typing import Any, Dict, Type, TypeVar

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import Union
import datetime


T = TypeVar("T", bound="EdgeCalibrationReport")


@_attrs_define
class EdgeCalibrationReport:
    """Calibration report built by core/edge/calibration from the decisions
    recorded on Edge events. An open object: the fields below are always
    present, and other report fields may be added without a schema change.

        Attributes:
            id (str): Content-derived id, cal- followed by 32 hex characters.
            tenant_id (Union[Unset, str]):
            since (Union[Unset, datetime.datetime]):
            until (Union[Unset, datetime.datetime]):
            sessions_observed (Union[Unset, int]): Sessions with at least one counted event.
            events_scanned (Union[Unset, int]): Distinct events of the window that were counted.
            truncated (Union[Unset, bool]): Set when a scan cap stopped the scan early.
    """

    id: str
    tenant_id: Union[Unset, str] = UNSET
    since: Union[Unset, datetime.datetime] = UNSET
    until: Union[Unset, datetime.datetime] = UNSET
    sessions_observed: Union[Unset, int] = UNSET
    events_scanned: Union[Unset, int] = UNSET
    truncated: Union[Unset, bool] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        id = self.id

        tenant_id = self.tenant_id

        since: Union[Unset, str] = UNSET
        if not isinstance(self.since, Unset):
            since = self.since.isoformat()

        until: Union[Unset, str] = UNSET
        if not isinstance(self.until, Unset):
            until = self.until.isoformat()

        sessions_observed = self.sessions_observed

        events_scanned = self.events_scanned

        truncated = self.truncated

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
            }
        )
        if tenant_id is not UNSET:
            field_dict["tenant_id"] = tenant_id
        if since is not UNSET:
            field_dict["since"] = since
        if until is not UNSET:
            field_dict["until"] = until
        if sessions_observed is not UNSET:
            field_dict["sessions_observed"] = sessions_observed
        if events_scanned is not UNSET:
            field_dict["events_scanned"] = events_scanned
        if truncated is not UNSET:
            field_dict["truncated"] = truncated

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        id = d.pop("id")

        tenant_id = d.pop("tenant_id", UNSET)

        _since = d.pop("since", UNSET)
        since: Union[Unset, datetime.datetime]
        if isinstance(_since, Unset):
            since = UNSET
        else:
            since = isoparse(_since)

        _until = d.pop("until", UNSET)
        until: Union[Unset, datetime.datetime]
        if isinstance(_until, Unset):
            until = UNSET
        else:
            until = isoparse(_until)

        sessions_observed = d.pop("sessions_observed", UNSET)

        events_scanned = d.pop("events_scanned", UNSET)

        truncated = d.pop("truncated", UNSET)

        edge_calibration_report = cls(
            id=id,
            tenant_id=tenant_id,
            since=since,
            until=until,
            sessions_observed=sessions_observed,
            events_scanned=events_scanned,
            truncated=truncated,
        )

        edge_calibration_report.additional_properties = d
        return edge_calibration_report

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
