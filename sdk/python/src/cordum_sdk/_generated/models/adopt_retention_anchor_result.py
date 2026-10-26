from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.adopt_retention_anchor_result_previous_anchor_state import AdoptRetentionAnchorResultPreviousAnchorState


T = TypeVar("T", bound="AdoptRetentionAnchorResult")


@_attrs_define
class AdoptRetentionAnchorResult:
    """
    Attributes:
        tenant (str):
        adopted_seq (int): Seq of the adopted anchor, one below the oldest retained event.
        adopted_hash (str): Event hash the oldest retained event links to (its `prev_hash`).
        adopted_stream_id (str): Redis Stream ID just below the oldest retained entry.
        previous_anchor (str): The replaced anchor value; empty when there was none.
        previous_anchor_state (AdoptRetentionAnchorResultPreviousAnchorState):
        event_seq (int): Seq of the chain event that records the adoption.
    """

    tenant: str
    adopted_seq: int
    adopted_hash: str
    adopted_stream_id: str
    previous_anchor: str
    previous_anchor_state: AdoptRetentionAnchorResultPreviousAnchorState
    event_seq: int
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        tenant = self.tenant

        adopted_seq = self.adopted_seq

        adopted_hash = self.adopted_hash

        adopted_stream_id = self.adopted_stream_id

        previous_anchor = self.previous_anchor

        previous_anchor_state = self.previous_anchor_state.value

        event_seq = self.event_seq

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "tenant": tenant,
                "adopted_seq": adopted_seq,
                "adopted_hash": adopted_hash,
                "adopted_stream_id": adopted_stream_id,
                "previous_anchor": previous_anchor,
                "previous_anchor_state": previous_anchor_state,
                "event_seq": event_seq,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        tenant = d.pop("tenant")

        adopted_seq = d.pop("adopted_seq")

        adopted_hash = d.pop("adopted_hash")

        adopted_stream_id = d.pop("adopted_stream_id")

        previous_anchor = d.pop("previous_anchor")

        previous_anchor_state = AdoptRetentionAnchorResultPreviousAnchorState(d.pop("previous_anchor_state"))

        event_seq = d.pop("event_seq")

        adopt_retention_anchor_result = cls(
            tenant=tenant,
            adopted_seq=adopted_seq,
            adopted_hash=adopted_hash,
            adopted_stream_id=adopted_stream_id,
            previous_anchor=previous_anchor,
            previous_anchor_state=previous_anchor_state,
            event_seq=event_seq,
        )

        adopt_retention_anchor_result.additional_properties = d
        return adopt_retention_anchor_result

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
