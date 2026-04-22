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
  from ..models.list_eval_dataset_versions_response_200_items_item import ListEvalDatasetVersionsResponse200ItemsItem





T = TypeVar("T", bound="ListEvalDatasetVersionsResponse200")


@_attrs_define
class ListEvalDatasetVersionsResponse200:
    """ 
        Attributes:
            items (Union[Unset, List['ListEvalDatasetVersionsResponse200ItemsItem']]):
     """

    items: Union[Unset, List['ListEvalDatasetVersionsResponse200ItemsItem']] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)


    def to_dict(self) -> Dict[str, Any]:
        from ..models.list_eval_dataset_versions_response_200_items_item import ListEvalDatasetVersionsResponse200ItemsItem
        items: Union[Unset, List[Dict[str, Any]]] = UNSET
        if not isinstance(self.items, Unset):
            items = []
            for items_item_data in self.items:
                items_item = items_item_data.to_dict()
                items.append(items_item)




        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
        })
        if items is not UNSET:
            field_dict["items"] = items

        return field_dict



    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.list_eval_dataset_versions_response_200_items_item import ListEvalDatasetVersionsResponse200ItemsItem
        d = src_dict.copy()
        items = []
        _items = d.pop("items", UNSET)
        for items_item_data in (_items or []):
            items_item = ListEvalDatasetVersionsResponse200ItemsItem.from_dict(items_item_data)



            items.append(items_item)


        list_eval_dataset_versions_response_200 = cls(
            items=items,
        )


        list_eval_dataset_versions_response_200.additional_properties = d
        return list_eval_dataset_versions_response_200

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
