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
    from ..models.shadow_comparison_entry import ShadowComparisonEntry


T = TypeVar("T", bound="ShadowComparisonsResponse")


@_attrs_define
class ShadowComparisonsResponse:
    """
    Attributes:
        entries (List['ShadowComparisonEntry']):
        truncated_at_max (bool):
        next_cursor (Union[Unset, str]): Redis stream ID (`<ms>-<seq>`) to pass back as `?cursor=` for the next page.
            Empty when no more rows.
    """

    entries: List["ShadowComparisonEntry"]
    truncated_at_max: bool
    next_cursor: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.shadow_comparison_entry import ShadowComparisonEntry

        entries = []
        for entries_item_data in self.entries:
            entries_item = entries_item_data.to_dict()
            entries.append(entries_item)

        truncated_at_max = self.truncated_at_max

        next_cursor = self.next_cursor

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "entries": entries,
                "truncated_at_max": truncated_at_max,
            }
        )
        if next_cursor is not UNSET:
            field_dict["next_cursor"] = next_cursor

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.shadow_comparison_entry import ShadowComparisonEntry

        d = src_dict.copy()
        entries = []
        _entries = d.pop("entries")
        for entries_item_data in _entries:
            entries_item = ShadowComparisonEntry.from_dict(entries_item_data)

            entries.append(entries_item)

        truncated_at_max = d.pop("truncated_at_max")

        next_cursor = d.pop("next_cursor", UNSET)

        shadow_comparisons_response = cls(
            entries=entries,
            truncated_at_max=truncated_at_max,
            next_cursor=next_cursor,
        )

        shadow_comparisons_response.additional_properties = d
        return shadow_comparisons_response

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
