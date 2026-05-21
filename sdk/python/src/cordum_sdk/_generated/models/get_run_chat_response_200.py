from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast
from typing import cast, List
from typing import cast, Union
from typing import Dict
from typing import Union

if TYPE_CHECKING:
    from ..models.chat_message import ChatMessage


T = TypeVar("T", bound="GetRunChatResponse200")


@_attrs_define
class GetRunChatResponse200:
    """
    Attributes:
        next_cursor (Union[None, Unset, str]): Cursor for the next page (null if no more pages)
        items (Union[Unset, List['ChatMessage']]):
    """

    next_cursor: Union[None, Unset, str] = UNSET
    items: Union[Unset, List["ChatMessage"]] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.chat_message import ChatMessage

        next_cursor: Union[None, Unset, str]
        if isinstance(self.next_cursor, Unset):
            next_cursor = UNSET
        else:
            next_cursor = self.next_cursor

        items: Union[Unset, List[Dict[str, Any]]] = UNSET
        if not isinstance(self.items, Unset):
            items = []
            for items_item_data in self.items:
                items_item = items_item_data.to_dict()
                items.append(items_item)

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if next_cursor is not UNSET:
            field_dict["next_cursor"] = next_cursor
        if items is not UNSET:
            field_dict["items"] = items

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.chat_message import ChatMessage

        d = src_dict.copy()

        def _parse_next_cursor(data: object) -> Union[None, Unset, str]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, str], data)

        next_cursor = _parse_next_cursor(d.pop("next_cursor", UNSET))

        items = []
        _items = d.pop("items", UNSET)
        for items_item_data in _items or []:
            items_item = ChatMessage.from_dict(items_item_data)

            items.append(items_item)

        get_run_chat_response_200 = cls(
            next_cursor=next_cursor,
            items=items,
        )

        get_run_chat_response_200.additional_properties = d
        return get_run_chat_response_200

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
