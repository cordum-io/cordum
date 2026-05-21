from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import Union


T = TypeVar("T", bound="UpdatePolicyBundleRequest")


@_attrs_define
class UpdatePolicyBundleRequest:
    """
    Attributes:
        content (Union[Unset, str]):
        enabled (Union[Unset, bool]):
        author (Union[Unset, str]):
        message (Union[Unset, str]):
    """

    content: Union[Unset, str] = UNSET
    enabled: Union[Unset, bool] = UNSET
    author: Union[Unset, str] = UNSET
    message: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        content = self.content

        enabled = self.enabled

        author = self.author

        message = self.message

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if content is not UNSET:
            field_dict["content"] = content
        if enabled is not UNSET:
            field_dict["enabled"] = enabled
        if author is not UNSET:
            field_dict["author"] = author
        if message is not UNSET:
            field_dict["message"] = message

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        content = d.pop("content", UNSET)

        enabled = d.pop("enabled", UNSET)

        author = d.pop("author", UNSET)

        message = d.pop("message", UNSET)

        update_policy_bundle_request = cls(
            content=content,
            enabled=enabled,
            author=author,
            message=message,
        )

        update_policy_bundle_request.additional_properties = d
        return update_policy_bundle_request

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
