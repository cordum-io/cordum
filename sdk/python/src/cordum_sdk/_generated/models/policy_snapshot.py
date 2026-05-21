from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import cast, List
from typing import cast, Union
from typing import Union
import datetime


T = TypeVar("T", bound="PolicySnapshot")


@_attrs_define
class PolicySnapshot:
    """
    Attributes:
        id (Union[Unset, str]):
        note (Union[Unset, str]):
        author (Union[Unset, str]):
        created_at (Union[Unset, datetime.datetime]):
        bundle_ids (Union[List[str], None, Unset]):
    """

    id: Union[Unset, str] = UNSET
    note: Union[Unset, str] = UNSET
    author: Union[Unset, str] = UNSET
    created_at: Union[Unset, datetime.datetime] = UNSET
    bundle_ids: Union[List[str], None, Unset] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        id = self.id

        note = self.note

        author = self.author

        created_at: Union[Unset, str] = UNSET
        if not isinstance(self.created_at, Unset):
            created_at = self.created_at.isoformat()

        bundle_ids: Union[List[str], None, Unset]
        if isinstance(self.bundle_ids, Unset):
            bundle_ids = UNSET
        elif isinstance(self.bundle_ids, list):
            bundle_ids = self.bundle_ids

        else:
            bundle_ids = self.bundle_ids

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if id is not UNSET:
            field_dict["id"] = id
        if note is not UNSET:
            field_dict["note"] = note
        if author is not UNSET:
            field_dict["author"] = author
        if created_at is not UNSET:
            field_dict["created_at"] = created_at
        if bundle_ids is not UNSET:
            field_dict["bundle_ids"] = bundle_ids

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        id = d.pop("id", UNSET)

        note = d.pop("note", UNSET)

        author = d.pop("author", UNSET)

        _created_at = d.pop("created_at", UNSET)
        created_at: Union[Unset, datetime.datetime]
        if isinstance(_created_at, Unset):
            created_at = UNSET
        else:
            created_at = isoparse(_created_at)

        def _parse_bundle_ids(data: object) -> Union[List[str], None, Unset]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, list):
                    raise TypeError()
                bundle_ids_type_0 = cast(List[str], data)

                return bundle_ids_type_0
            except:  # noqa: E722
                pass
            return cast(Union[List[str], None, Unset], data)

        bundle_ids = _parse_bundle_ids(d.pop("bundle_ids", UNSET))

        policy_snapshot = cls(
            id=id,
            note=note,
            author=author,
            created_at=created_at,
            bundle_ids=bundle_ids,
        )

        policy_snapshot.additional_properties = d
        return policy_snapshot

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
