from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast
from typing import cast, List
from typing import Dict
from typing import Union

if TYPE_CHECKING:
    from ..models.edge_runtime_event_envelope import EdgeRuntimeEventEnvelope
    from ..models.edge_runtime_ingest_source import EdgeRuntimeIngestSource


T = TypeVar("T", bound="EdgeRuntimeIngestRequest")


@_attrs_define
class EdgeRuntimeIngestRequest:
    """
    Attributes:
        source (EdgeRuntimeIngestSource): Authenticated trusted sidecar identity that produced the batch.
        nonce (str): Required by default replay-protection nonce (UUIDv7 or random 128-bit value). Set
            `CORDUM_EDGE_RUNTIME_REPLAY_REQUIRED=false` only for transitional non-production clients that cannot yet send
            this field.
        events (List['EdgeRuntimeEventEnvelope']):
        batch_id (Union[Unset, str]): Operator correlation identifier only; it is not used for replay protection.
    """

    source: "EdgeRuntimeIngestSource"
    nonce: str
    events: List["EdgeRuntimeEventEnvelope"]
    batch_id: Union[Unset, str] = UNSET

    def to_dict(self) -> Dict[str, Any]:
        from ..models.edge_runtime_event_envelope import EdgeRuntimeEventEnvelope
        from ..models.edge_runtime_ingest_source import EdgeRuntimeIngestSource

        source = self.source.to_dict()

        nonce = self.nonce

        events = []
        for events_item_data in self.events:
            events_item = events_item_data.to_dict()
            events.append(events_item)

        batch_id = self.batch_id

        field_dict: Dict[str, Any] = {}
        field_dict.update(
            {
                "source": source,
                "nonce": nonce,
                "events": events,
            }
        )
        if batch_id is not UNSET:
            field_dict["batch_id"] = batch_id

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.edge_runtime_event_envelope import EdgeRuntimeEventEnvelope
        from ..models.edge_runtime_ingest_source import EdgeRuntimeIngestSource

        d = src_dict.copy()
        source = EdgeRuntimeIngestSource.from_dict(d.pop("source"))

        nonce = d.pop("nonce")

        events = []
        _events = d.pop("events")
        for events_item_data in _events:
            events_item = EdgeRuntimeEventEnvelope.from_dict(events_item_data)

            events.append(events_item)

        batch_id = d.pop("batch_id", UNSET)

        edge_runtime_ingest_request = cls(
            source=source,
            nonce=nonce,
            events=events,
            batch_id=batch_id,
        )

        return edge_runtime_ingest_request
