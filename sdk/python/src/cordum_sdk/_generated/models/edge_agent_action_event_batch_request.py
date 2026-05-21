from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from typing import cast
from typing import cast, List
from typing import Dict

if TYPE_CHECKING:
    from ..models.edge_agent_action_event_write_request import EdgeAgentActionEventWriteRequest


T = TypeVar("T", bound="EdgeAgentActionEventBatchRequest")


@_attrs_define
class EdgeAgentActionEventBatchRequest:
    """
    Attributes:
        events (List['EdgeAgentActionEventWriteRequest']):
    """

    events: List["EdgeAgentActionEventWriteRequest"]
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.edge_agent_action_event_write_request import EdgeAgentActionEventWriteRequest

        events = []
        for events_item_data in self.events:
            events_item = events_item_data.to_dict()
            events.append(events_item)

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "events": events,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.edge_agent_action_event_write_request import EdgeAgentActionEventWriteRequest

        d = src_dict.copy()
        events = []
        _events = d.pop("events")
        for events_item_data in _events:
            events_item = EdgeAgentActionEventWriteRequest.from_dict(events_item_data)

            events.append(events_item)

        edge_agent_action_event_batch_request = cls(
            events=events,
        )

        edge_agent_action_event_batch_request.additional_properties = d
        return edge_agent_action_event_batch_request

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
