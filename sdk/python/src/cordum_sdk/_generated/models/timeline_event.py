from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import cast, Union
from typing import Dict
from typing import Union
import datetime

if TYPE_CHECKING:
    from ..models.timeline_event_data_type_0 import TimelineEventDataType0


T = TypeVar("T", bound="TimelineEvent")


@_attrs_define
class TimelineEvent:
    """
    Attributes:
        timestamp (Union[Unset, datetime.datetime]):
        type (Union[Unset, str]):
        step_id (Union[None, Unset, str]):
        message (Union[Unset, str]):
        data (Union['TimelineEventDataType0', None, Unset]):
    """

    timestamp: Union[Unset, datetime.datetime] = UNSET
    type: Union[Unset, str] = UNSET
    step_id: Union[None, Unset, str] = UNSET
    message: Union[Unset, str] = UNSET
    data: Union["TimelineEventDataType0", None, Unset] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.timeline_event_data_type_0 import TimelineEventDataType0

        timestamp: Union[Unset, str] = UNSET
        if not isinstance(self.timestamp, Unset):
            timestamp = self.timestamp.isoformat()

        type = self.type

        step_id: Union[None, Unset, str]
        if isinstance(self.step_id, Unset):
            step_id = UNSET
        else:
            step_id = self.step_id

        message = self.message

        data: Union[Dict[str, Any], None, Unset]
        if isinstance(self.data, Unset):
            data = UNSET
        elif isinstance(self.data, TimelineEventDataType0):
            data = self.data.to_dict()
        else:
            data = self.data

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if timestamp is not UNSET:
            field_dict["timestamp"] = timestamp
        if type is not UNSET:
            field_dict["type"] = type
        if step_id is not UNSET:
            field_dict["step_id"] = step_id
        if message is not UNSET:
            field_dict["message"] = message
        if data is not UNSET:
            field_dict["data"] = data

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.timeline_event_data_type_0 import TimelineEventDataType0

        d = src_dict.copy()
        _timestamp = d.pop("timestamp", UNSET)
        timestamp: Union[Unset, datetime.datetime]
        if isinstance(_timestamp, Unset):
            timestamp = UNSET
        else:
            timestamp = isoparse(_timestamp)

        type = d.pop("type", UNSET)

        def _parse_step_id(data: object) -> Union[None, Unset, str]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, str], data)

        step_id = _parse_step_id(d.pop("step_id", UNSET))

        message = d.pop("message", UNSET)

        def _parse_data(data: object) -> Union["TimelineEventDataType0", None, Unset]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                data_type_0 = TimelineEventDataType0.from_dict(data)

                return data_type_0
            except:  # noqa: E722
                pass
            return cast(Union["TimelineEventDataType0", None, Unset], data)

        data = _parse_data(d.pop("data", UNSET))

        timeline_event = cls(
            timestamp=timestamp,
            type=type,
            step_id=step_id,
            message=message,
            data=data,
        )

        timeline_event.additional_properties = d
        return timeline_event

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
