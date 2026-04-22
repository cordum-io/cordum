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
  from ..models.delegation_view import DelegationView





T = TypeVar("T", bound="DelegationListResponse")


@_attrs_define
class DelegationListResponse:
    """ 
        Attributes:
            items (List['DelegationView']):
            next_cursor (Union[None, Unset, str]):
     """

    items: List['DelegationView']
    next_cursor: Union[None, Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)


    def to_dict(self) -> Dict[str, Any]:
        from ..models.delegation_view import DelegationView
        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)



        next_cursor: Union[None, Unset, str]
        if isinstance(self.next_cursor, Unset):
            next_cursor = UNSET
        else:
            next_cursor = self.next_cursor


        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
            "items": items,
        })
        if next_cursor is not UNSET:
            field_dict["next_cursor"] = next_cursor

        return field_dict



    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.delegation_view import DelegationView
        d = src_dict.copy()
        items = []
        _items = d.pop("items")
        for items_item_data in (_items):
            items_item = DelegationView.from_dict(items_item_data)



            items.append(items_item)


        def _parse_next_cursor(data: object) -> Union[None, Unset, str]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, str], data)

        next_cursor = _parse_next_cursor(d.pop("next_cursor", UNSET))


        delegation_list_response = cls(
            items=items,
            next_cursor=next_cursor,
        )


        delegation_list_response.additional_properties = d
        return delegation_list_response

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
