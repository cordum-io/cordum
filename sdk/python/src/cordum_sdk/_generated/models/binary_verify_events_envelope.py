from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from typing import cast
from typing import cast, List
from typing import Dict

if TYPE_CHECKING:
    from ..models.binary_verify_list_item import BinaryVerifyListItem


T = TypeVar("T", bound="BinaryVerifyEventsEnvelope")


@_attrs_define
class BinaryVerifyEventsEnvelope:
    """Paginated envelope returned by `GET /api/v1/edge/binary-integrity/events`.
    `next_cursor` is opaque; pass it back as `?cursor=` on the next
    request. Empty `next_cursor` means end-of-stream — clients should
    stop polling.

        Attributes:
            items (List['BinaryVerifyListItem']):
            next_cursor (str): Opaque cursor for the next page; empty when no more events.
            returned (int): Number of items in this page (== `items.length`).
    """

    items: List["BinaryVerifyListItem"]
    next_cursor: str
    returned: int
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.binary_verify_list_item import BinaryVerifyListItem

        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        next_cursor = self.next_cursor

        returned = self.returned

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "items": items,
                "next_cursor": next_cursor,
                "returned": returned,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.binary_verify_list_item import BinaryVerifyListItem

        d = src_dict.copy()
        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = BinaryVerifyListItem.from_dict(items_item_data)

            items.append(items_item)

        next_cursor = d.pop("next_cursor")

        returned = d.pop("returned")

        binary_verify_events_envelope = cls(
            items=items,
            next_cursor=next_cursor,
            returned=returned,
        )

        binary_verify_events_envelope.additional_properties = d
        return binary_verify_events_envelope

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
