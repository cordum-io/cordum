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
  from ..models.edge_llm_event_decision import EdgeLLMEventDecision





T = TypeVar("T", bound="EdgeLLMIngestResponse")


@_attrs_define
class EdgeLLMIngestResponse:
    """ 
        Attributes:
            accepted_count (int):
            decisions (Union[Unset, List['EdgeLLMEventDecision']]):
            replayed (Union[Unset, bool]): True when a duplicate nonce was suppressed and no events were appended. Default:
                False.
     """

    accepted_count: int
    decisions: Union[Unset, List['EdgeLLMEventDecision']] = UNSET
    replayed: Union[Unset, bool] = False
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)


    def to_dict(self) -> Dict[str, Any]:
        from ..models.edge_llm_event_decision import EdgeLLMEventDecision
        accepted_count = self.accepted_count

        decisions: Union[Unset, List[Dict[str, Any]]] = UNSET
        if not isinstance(self.decisions, Unset):
            decisions = []
            for decisions_item_data in self.decisions:
                decisions_item = decisions_item_data.to_dict()
                decisions.append(decisions_item)



        replayed = self.replayed


        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
            "accepted_count": accepted_count,
        })
        if decisions is not UNSET:
            field_dict["decisions"] = decisions
        if replayed is not UNSET:
            field_dict["replayed"] = replayed

        return field_dict



    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.edge_llm_event_decision import EdgeLLMEventDecision
        d = src_dict.copy()
        accepted_count = d.pop("accepted_count")

        decisions = []
        _decisions = d.pop("decisions", UNSET)
        for decisions_item_data in (_decisions or []):
            decisions_item = EdgeLLMEventDecision.from_dict(decisions_item_data)



            decisions.append(decisions_item)


        replayed = d.pop("replayed", UNSET)

        edge_llm_ingest_response = cls(
            accepted_count=accepted_count,
            decisions=decisions,
            replayed=replayed,
        )


        edge_llm_ingest_response.additional_properties = d
        return edge_llm_ingest_response

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
