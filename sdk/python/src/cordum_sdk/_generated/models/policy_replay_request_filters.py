from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import Union


T = TypeVar("T", bound="PolicyReplayRequestFilters")


@_attrs_define
class PolicyReplayRequestFilters:
    """
    Attributes:
        tenant (Union[Unset, str]):
        topic_pattern (Union[Unset, str]):
        original_decision (Union[Unset, str]):
    """

    tenant: Union[Unset, str] = UNSET
    topic_pattern: Union[Unset, str] = UNSET
    original_decision: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        tenant = self.tenant

        topic_pattern = self.topic_pattern

        original_decision = self.original_decision

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if tenant is not UNSET:
            field_dict["tenant"] = tenant
        if topic_pattern is not UNSET:
            field_dict["topic_pattern"] = topic_pattern
        if original_decision is not UNSET:
            field_dict["original_decision"] = original_decision

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        tenant = d.pop("tenant", UNSET)

        topic_pattern = d.pop("topic_pattern", UNSET)

        original_decision = d.pop("original_decision", UNSET)

        policy_replay_request_filters = cls(
            tenant=tenant,
            topic_pattern=topic_pattern,
            original_decision=original_decision,
        )

        policy_replay_request_filters.additional_properties = d
        return policy_replay_request_filters

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
