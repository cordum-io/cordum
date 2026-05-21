from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast, List
from typing import Union


T = TypeVar("T", bound="PublishPolicyRequest")


@_attrs_define
class PublishPolicyRequest:
    """
    Attributes:
        bundle_ids (List[str]):
        author (str):
        message (Union[Unset, str]):
        note (Union[Unset, str]):
    """

    bundle_ids: List[str]
    author: str
    message: Union[Unset, str] = UNSET
    note: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        bundle_ids = self.bundle_ids

        author = self.author

        message = self.message

        note = self.note

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "bundle_ids": bundle_ids,
                "author": author,
            }
        )
        if message is not UNSET:
            field_dict["message"] = message
        if note is not UNSET:
            field_dict["note"] = note

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        bundle_ids = cast(List[str], d.pop("bundle_ids"))

        author = d.pop("author")

        message = d.pop("message", UNSET)

        note = d.pop("note", UNSET)

        publish_policy_request = cls(
            bundle_ids=bundle_ids,
            author=author,
            message=message,
            note=note,
        )

        publish_policy_request.additional_properties = d
        return publish_policy_request

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
