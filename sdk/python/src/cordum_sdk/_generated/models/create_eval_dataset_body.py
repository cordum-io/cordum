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
    from ..models.create_eval_dataset_body_entries_item import CreateEvalDatasetBodyEntriesItem


T = TypeVar("T", bound="CreateEvalDatasetBody")


@_attrs_define
class CreateEvalDatasetBody:
    """
    Attributes:
        name (str):
        version (int):
        entries (List['CreateEvalDatasetBodyEntriesItem']):
        description (Union[Unset, str]):
    """

    name: str
    version: int
    entries: List["CreateEvalDatasetBodyEntriesItem"]
    description: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.create_eval_dataset_body_entries_item import CreateEvalDatasetBodyEntriesItem

        name = self.name

        version = self.version

        entries = []
        for entries_item_data in self.entries:
            entries_item = entries_item_data.to_dict()
            entries.append(entries_item)

        description = self.description

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "version": version,
                "entries": entries,
            }
        )
        if description is not UNSET:
            field_dict["description"] = description

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.create_eval_dataset_body_entries_item import CreateEvalDatasetBodyEntriesItem

        d = src_dict.copy()
        name = d.pop("name")

        version = d.pop("version")

        entries = []
        _entries = d.pop("entries")
        for entries_item_data in _entries:
            entries_item = CreateEvalDatasetBodyEntriesItem.from_dict(entries_item_data)

            entries.append(entries_item)

        description = d.pop("description", UNSET)

        create_eval_dataset_body = cls(
            name=name,
            version=version,
            entries=entries,
            description=description,
        )

        create_eval_dataset_body.additional_properties = d
        return create_eval_dataset_body

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
