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
    from ..models.edge_runtime_ingest_drop_report import EdgeRuntimeIngestDropReport


T = TypeVar("T", bound="EdgeRuntimeIngestResponse")


@_attrs_define
class EdgeRuntimeIngestResponse:
    """
    Attributes:
        accepted_count (int):
        dropped_count (int):
        dropped (Union[Unset, List['EdgeRuntimeIngestDropReport']]):
    """

    accepted_count: int
    dropped_count: int
    dropped: Union[Unset, List["EdgeRuntimeIngestDropReport"]] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.edge_runtime_ingest_drop_report import EdgeRuntimeIngestDropReport

        accepted_count = self.accepted_count

        dropped_count = self.dropped_count

        dropped: Union[Unset, List[Dict[str, Any]]] = UNSET
        if not isinstance(self.dropped, Unset):
            dropped = []
            for dropped_item_data in self.dropped:
                dropped_item = dropped_item_data.to_dict()
                dropped.append(dropped_item)

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "accepted_count": accepted_count,
                "dropped_count": dropped_count,
            }
        )
        if dropped is not UNSET:
            field_dict["dropped"] = dropped

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.edge_runtime_ingest_drop_report import EdgeRuntimeIngestDropReport

        d = src_dict.copy()
        accepted_count = d.pop("accepted_count")

        dropped_count = d.pop("dropped_count")

        dropped = []
        _dropped = d.pop("dropped", UNSET)
        for dropped_item_data in _dropped or []:
            dropped_item = EdgeRuntimeIngestDropReport.from_dict(dropped_item_data)

            dropped.append(dropped_item)

        edge_runtime_ingest_response = cls(
            accepted_count=accepted_count,
            dropped_count=dropped_count,
            dropped=dropped,
        )

        edge_runtime_ingest_response.additional_properties = d
        return edge_runtime_ingest_response

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
