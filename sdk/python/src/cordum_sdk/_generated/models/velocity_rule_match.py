from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast, List
from typing import Union


T = TypeVar("T", bound="VelocityRuleMatch")


@_attrs_define
class VelocityRuleMatch:
    """
    Attributes:
        topics (Union[Unset, List[str]]):
        tenants (Union[Unset, List[str]]):
        risk_tags (Union[Unset, List[str]]):
    """

    topics: Union[Unset, List[str]] = UNSET
    tenants: Union[Unset, List[str]] = UNSET
    risk_tags: Union[Unset, List[str]] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        topics: Union[Unset, List[str]] = UNSET
        if not isinstance(self.topics, Unset):
            topics = self.topics

        tenants: Union[Unset, List[str]] = UNSET
        if not isinstance(self.tenants, Unset):
            tenants = self.tenants

        risk_tags: Union[Unset, List[str]] = UNSET
        if not isinstance(self.risk_tags, Unset):
            risk_tags = self.risk_tags

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if topics is not UNSET:
            field_dict["topics"] = topics
        if tenants is not UNSET:
            field_dict["tenants"] = tenants
        if risk_tags is not UNSET:
            field_dict["risk_tags"] = risk_tags

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        topics = cast(List[str], d.pop("topics", UNSET))

        tenants = cast(List[str], d.pop("tenants", UNSET))

        risk_tags = cast(List[str], d.pop("risk_tags", UNSET))

        velocity_rule_match = cls(
            topics=topics,
            tenants=tenants,
            risk_tags=risk_tags,
        )

        velocity_rule_match.additional_properties = d
        return velocity_rule_match

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
