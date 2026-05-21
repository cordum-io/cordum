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
    from ..models.topic_response import TopicResponse


T = TypeVar("T", bound="ListTopicsResponse200")


@_attrs_define
class ListTopicsResponse200:
    """
    Attributes:
        items (List['TopicResponse']):
        registry_empty (Union[Unset, bool]):
    """

    items: List["TopicResponse"]
    registry_empty: Union[Unset, bool] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.topic_response import TopicResponse

        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        registry_empty = self.registry_empty

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "items": items,
            }
        )
        if registry_empty is not UNSET:
            field_dict["registry_empty"] = registry_empty

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.topic_response import TopicResponse

        d = src_dict.copy()
        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = TopicResponse.from_dict(items_item_data)

            items.append(items_item)

        registry_empty = d.pop("registry_empty", UNSET)

        list_topics_response_200 = cls(
            items=items,
            registry_empty=registry_empty,
        )

        list_topics_response_200.additional_properties = d
        return list_topics_response_200

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
