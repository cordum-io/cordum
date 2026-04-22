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
  from ..models.create_eval_dataset_successor_body_entries_item import CreateEvalDatasetSuccessorBodyEntriesItem





T = TypeVar("T", bound="CreateEvalDatasetSuccessorBody")


@_attrs_define
class CreateEvalDatasetSuccessorBody:
    """ 
        Attributes:
            version (Union[Unset, int]):
            description (Union[Unset, str]):
            entries (Union[Unset, List['CreateEvalDatasetSuccessorBodyEntriesItem']]):
     """

    version: Union[Unset, int] = UNSET
    description: Union[Unset, str] = UNSET
    entries: Union[Unset, List['CreateEvalDatasetSuccessorBodyEntriesItem']] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)


    def to_dict(self) -> Dict[str, Any]:
        from ..models.create_eval_dataset_successor_body_entries_item import CreateEvalDatasetSuccessorBodyEntriesItem
        version = self.version

        description = self.description

        entries: Union[Unset, List[Dict[str, Any]]] = UNSET
        if not isinstance(self.entries, Unset):
            entries = []
            for entries_item_data in self.entries:
                entries_item = entries_item_data.to_dict()
                entries.append(entries_item)




        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
        })
        if version is not UNSET:
            field_dict["version"] = version
        if description is not UNSET:
            field_dict["description"] = description
        if entries is not UNSET:
            field_dict["entries"] = entries

        return field_dict



    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.create_eval_dataset_successor_body_entries_item import CreateEvalDatasetSuccessorBodyEntriesItem
        d = src_dict.copy()
        version = d.pop("version", UNSET)

        description = d.pop("description", UNSET)

        entries = []
        _entries = d.pop("entries", UNSET)
        for entries_item_data in (_entries or []):
            entries_item = CreateEvalDatasetSuccessorBodyEntriesItem.from_dict(entries_item_data)



            entries.append(entries_item)


        create_eval_dataset_successor_body = cls(
            version=version,
            description=description,
            entries=entries,
        )


        create_eval_dataset_successor_body.additional_properties = d
        return create_eval_dataset_successor_body

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
