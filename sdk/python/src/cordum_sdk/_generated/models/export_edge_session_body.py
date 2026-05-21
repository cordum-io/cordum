from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import Union


T = TypeVar("T", bound="ExportEdgeSessionBody")


@_attrs_define
class ExportEdgeSessionBody:
    """
    Attributes:
        max_events (Union[Unset, int]): Caps the number of events the bundle carries. When the
            session has more, `truncation.events_truncated = true`
            and `truncation.event_count` records the actual total.
            Values above 10000 are rejected with HTTP 400 +
            `code=max_events_too_large` (EDGE-065 request-validation
            bound). Values <= 0 fall back to the assembler default
            (5000).
             Default: 5000.
    """

    max_events: Union[Unset, int] = 5000

    def to_dict(self) -> Dict[str, Any]:
        max_events = self.max_events

        field_dict: Dict[str, Any] = {}
        field_dict.update({})
        if max_events is not UNSET:
            field_dict["max_events"] = max_events

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        max_events = d.pop("max_events", UNSET)

        export_edge_session_body = cls(
            max_events=max_events,
        )

        return export_edge_session_body
