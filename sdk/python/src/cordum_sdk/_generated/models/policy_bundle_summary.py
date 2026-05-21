from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import Union
import datetime


T = TypeVar("T", bound="PolicyBundleSummary")


@_attrs_define
class PolicyBundleSummary:
    """
    Attributes:
        id (Union[Unset, str]):
        enabled (Union[Unset, bool]):
        source (Union[Unset, str]):
        author (Union[Unset, str]):
        message (Union[Unset, str]):
        rule_count (Union[Unset, int]):
        updated_at (Union[Unset, datetime.datetime]):
    """

    id: Union[Unset, str] = UNSET
    enabled: Union[Unset, bool] = UNSET
    source: Union[Unset, str] = UNSET
    author: Union[Unset, str] = UNSET
    message: Union[Unset, str] = UNSET
    rule_count: Union[Unset, int] = UNSET
    updated_at: Union[Unset, datetime.datetime] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        id = self.id

        enabled = self.enabled

        source = self.source

        author = self.author

        message = self.message

        rule_count = self.rule_count

        updated_at: Union[Unset, str] = UNSET
        if not isinstance(self.updated_at, Unset):
            updated_at = self.updated_at.isoformat()

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if id is not UNSET:
            field_dict["id"] = id
        if enabled is not UNSET:
            field_dict["enabled"] = enabled
        if source is not UNSET:
            field_dict["source"] = source
        if author is not UNSET:
            field_dict["author"] = author
        if message is not UNSET:
            field_dict["message"] = message
        if rule_count is not UNSET:
            field_dict["rule_count"] = rule_count
        if updated_at is not UNSET:
            field_dict["updated_at"] = updated_at

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        id = d.pop("id", UNSET)

        enabled = d.pop("enabled", UNSET)

        source = d.pop("source", UNSET)

        author = d.pop("author", UNSET)

        message = d.pop("message", UNSET)

        rule_count = d.pop("rule_count", UNSET)

        _updated_at = d.pop("updated_at", UNSET)
        updated_at: Union[Unset, datetime.datetime]
        if isinstance(_updated_at, Unset):
            updated_at = UNSET
        else:
            updated_at = isoparse(_updated_at)

        policy_bundle_summary = cls(
            id=id,
            enabled=enabled,
            source=source,
            author=author,
            message=message,
            rule_count=rule_count,
            updated_at=updated_at,
        )

        policy_bundle_summary.additional_properties = d
        return policy_bundle_summary

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
