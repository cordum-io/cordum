from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast
from typing import cast, List
from typing import Dict
from typing import Union

if TYPE_CHECKING:
    from ..models.binary_verify_event import BinaryVerifyEvent


T = TypeVar("T", bound="IngestBinaryVerifyRequest")


@_attrs_define
class IngestBinaryVerifyRequest:
    """Batch envelope for binary-verify outcomes. Up to 1000 events per
    request; the body byte size is capped at 2 MB.

        Attributes:
            events (List['BinaryVerifyEvent']):
            endpoint (Union[Unset, str]): Optional operator-supplied label identifying the host that
                ran the install script. Captured into every persisted event.
    """

    events: List["BinaryVerifyEvent"]
    endpoint: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.binary_verify_event import BinaryVerifyEvent

        events = []
        for events_item_data in self.events:
            events_item = events_item_data.to_dict()
            events.append(events_item)

        endpoint = self.endpoint

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "events": events,
            }
        )
        if endpoint is not UNSET:
            field_dict["endpoint"] = endpoint

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.binary_verify_event import BinaryVerifyEvent

        d = src_dict.copy()
        events = []
        _events = d.pop("events")
        for events_item_data in _events:
            events_item = BinaryVerifyEvent.from_dict(events_item_data)

            events.append(events_item)

        endpoint = d.pop("endpoint", UNSET)

        ingest_binary_verify_request = cls(
            events=events,
            endpoint=endpoint,
        )

        ingest_binary_verify_request.additional_properties = d
        return ingest_binary_verify_request

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
