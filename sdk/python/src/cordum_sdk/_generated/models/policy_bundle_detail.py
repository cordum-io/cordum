from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import cast, List
from typing import Dict
from typing import Union
import datetime

if TYPE_CHECKING:
    from ..models.policy_rule import PolicyRule


T = TypeVar("T", bound="PolicyBundleDetail")


@_attrs_define
class PolicyBundleDetail:
    """
    Attributes:
        id (Union[Unset, str]):
        enabled (Union[Unset, bool]):
        source (Union[Unset, str]):
        author (Union[Unset, str]):
        message (Union[Unset, str]):
        rule_count (Union[Unset, int]):
        updated_at (Union[Unset, datetime.datetime]):
        content (Union[Unset, str]): Raw YAML content of the bundle
        rules (Union[Unset, List['PolicyRule']]):
    """

    id: Union[Unset, str] = UNSET
    enabled: Union[Unset, bool] = UNSET
    source: Union[Unset, str] = UNSET
    author: Union[Unset, str] = UNSET
    message: Union[Unset, str] = UNSET
    rule_count: Union[Unset, int] = UNSET
    updated_at: Union[Unset, datetime.datetime] = UNSET
    content: Union[Unset, str] = UNSET
    rules: Union[Unset, List["PolicyRule"]] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.policy_rule import PolicyRule

        id = self.id

        enabled = self.enabled

        source = self.source

        author = self.author

        message = self.message

        rule_count = self.rule_count

        updated_at: Union[Unset, str] = UNSET
        if not isinstance(self.updated_at, Unset):
            updated_at = self.updated_at.isoformat()

        content = self.content

        rules: Union[Unset, List[Dict[str, Any]]] = UNSET
        if not isinstance(self.rules, Unset):
            rules = []
            for rules_item_data in self.rules:
                rules_item = rules_item_data.to_dict()
                rules.append(rules_item)

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
        if content is not UNSET:
            field_dict["content"] = content
        if rules is not UNSET:
            field_dict["rules"] = rules

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.policy_rule import PolicyRule

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

        content = d.pop("content", UNSET)

        rules = []
        _rules = d.pop("rules", UNSET)
        for rules_item_data in _rules or []:
            rules_item = PolicyRule.from_dict(rules_item_data)

            rules.append(rules_item)

        policy_bundle_detail = cls(
            id=id,
            enabled=enabled,
            source=source,
            author=author,
            message=message,
            rule_count=rule_count,
            updated_at=updated_at,
            content=content,
            rules=rules,
        )

        policy_bundle_detail.additional_properties = d
        return policy_bundle_detail

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
