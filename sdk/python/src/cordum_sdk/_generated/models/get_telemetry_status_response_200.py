from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import Union
import datetime


T = TypeVar("T", bound="GetTelemetryStatusResponse200")


@_attrs_define
class GetTelemetryStatusResponse200:
    """
    Attributes:
        mode (str):
        endpoint (Union[Unset, str]):
        last_collected_at (Union[Unset, datetime.datetime]):
        last_reported_at (Union[Unset, datetime.datetime]):
    """

    mode: str
    endpoint: Union[Unset, str] = UNSET
    last_collected_at: Union[Unset, datetime.datetime] = UNSET
    last_reported_at: Union[Unset, datetime.datetime] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        mode = self.mode

        endpoint = self.endpoint

        last_collected_at: Union[Unset, str] = UNSET
        if not isinstance(self.last_collected_at, Unset):
            last_collected_at = self.last_collected_at.isoformat()

        last_reported_at: Union[Unset, str] = UNSET
        if not isinstance(self.last_reported_at, Unset):
            last_reported_at = self.last_reported_at.isoformat()

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "mode": mode,
            }
        )
        if endpoint is not UNSET:
            field_dict["endpoint"] = endpoint
        if last_collected_at is not UNSET:
            field_dict["last_collected_at"] = last_collected_at
        if last_reported_at is not UNSET:
            field_dict["last_reported_at"] = last_reported_at

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        mode = d.pop("mode")

        endpoint = d.pop("endpoint", UNSET)

        _last_collected_at = d.pop("last_collected_at", UNSET)
        last_collected_at: Union[Unset, datetime.datetime]
        if isinstance(_last_collected_at, Unset):
            last_collected_at = UNSET
        else:
            last_collected_at = isoparse(_last_collected_at)

        _last_reported_at = d.pop("last_reported_at", UNSET)
        last_reported_at: Union[Unset, datetime.datetime]
        if isinstance(_last_reported_at, Unset):
            last_reported_at = UNSET
        else:
            last_reported_at = isoparse(_last_reported_at)

        get_telemetry_status_response_200 = cls(
            mode=mode,
            endpoint=endpoint,
            last_collected_at=last_collected_at,
            last_reported_at=last_reported_at,
        )

        get_telemetry_status_response_200.additional_properties = d
        return get_telemetry_status_response_200

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
