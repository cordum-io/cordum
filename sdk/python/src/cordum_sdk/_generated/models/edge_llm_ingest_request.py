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
  from ..models.edge_llm_event_envelope import EdgeLLMEventEnvelope
  from ..models.edge_llm_ingest_source import EdgeLLMIngestSource





T = TypeVar("T", bound="EdgeLLMIngestRequest")


@_attrs_define
class EdgeLLMIngestRequest:
    """ 
        Attributes:
            source (EdgeLLMIngestSource):
            events (List['EdgeLLMEventEnvelope']):
            nonce (Union[Unset, str]): Optional replay-protection nonce. When present it is deduplicated against a Redis
                replay window scoped to `(tenant, llm-proxy)`. Set `CORDUM_EDGE_LLM_REPLAY_REQUIRED=true` to mandate it.
            batch_id (Union[Unset, str]): Operator correlation identifier only; not used for replay protection.
     """

    source: 'EdgeLLMIngestSource'
    events: List['EdgeLLMEventEnvelope']
    nonce: Union[Unset, str] = UNSET
    batch_id: Union[Unset, str] = UNSET


    def to_dict(self) -> Dict[str, Any]:
        from ..models.edge_llm_event_envelope import EdgeLLMEventEnvelope
        from ..models.edge_llm_ingest_source import EdgeLLMIngestSource
        source = self.source.to_dict()

        events = []
        for events_item_data in self.events:
            events_item = events_item_data.to_dict()
            events.append(events_item)



        nonce = self.nonce

        batch_id = self.batch_id


        field_dict: Dict[str, Any] = {}
        field_dict.update({
            "source": source,
            "events": events,
        })
        if nonce is not UNSET:
            field_dict["nonce"] = nonce
        if batch_id is not UNSET:
            field_dict["batch_id"] = batch_id

        return field_dict



    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.edge_llm_event_envelope import EdgeLLMEventEnvelope
        from ..models.edge_llm_ingest_source import EdgeLLMIngestSource
        d = src_dict.copy()
        source = EdgeLLMIngestSource.from_dict(d.pop("source"))




        events = []
        _events = d.pop("events")
        for events_item_data in (_events):
            events_item = EdgeLLMEventEnvelope.from_dict(events_item_data)



            events.append(events_item)


        nonce = d.pop("nonce", UNSET)

        batch_id = d.pop("batch_id", UNSET)

        edge_llm_ingest_request = cls(
            source=source,
            events=events,
            nonce=nonce,
            batch_id=batch_id,
        )

        return edge_llm_ingest_request

