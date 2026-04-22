from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast, List
from typing import Union






T = TypeVar("T", bound="IssueDelegationTokenBody")


@_attrs_define
class IssueDelegationTokenBody:
    """ 
        Attributes:
            target_agent_id (str):
            allowed_actions (Union[Unset, List[str]]):
            allowed_topics (Union[Unset, List[str]]):
            ttl_seconds (Union[Unset, int]):
            parent_token (Union[Unset, str]):
     """

    target_agent_id: str
    allowed_actions: Union[Unset, List[str]] = UNSET
    allowed_topics: Union[Unset, List[str]] = UNSET
    ttl_seconds: Union[Unset, int] = UNSET
    parent_token: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)


    def to_dict(self) -> Dict[str, Any]:
        target_agent_id = self.target_agent_id

        allowed_actions: Union[Unset, List[str]] = UNSET
        if not isinstance(self.allowed_actions, Unset):
            allowed_actions = self.allowed_actions



        allowed_topics: Union[Unset, List[str]] = UNSET
        if not isinstance(self.allowed_topics, Unset):
            allowed_topics = self.allowed_topics



        ttl_seconds = self.ttl_seconds

        parent_token = self.parent_token


        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
            "target_agent_id": target_agent_id,
        })
        if allowed_actions is not UNSET:
            field_dict["allowed_actions"] = allowed_actions
        if allowed_topics is not UNSET:
            field_dict["allowed_topics"] = allowed_topics
        if ttl_seconds is not UNSET:
            field_dict["ttl_seconds"] = ttl_seconds
        if parent_token is not UNSET:
            field_dict["parent_token"] = parent_token

        return field_dict



    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        target_agent_id = d.pop("target_agent_id")

        allowed_actions = cast(List[str], d.pop("allowed_actions", UNSET))


        allowed_topics = cast(List[str], d.pop("allowed_topics", UNSET))


        ttl_seconds = d.pop("ttl_seconds", UNSET)

        parent_token = d.pop("parent_token", UNSET)

        issue_delegation_token_body = cls(
            target_agent_id=target_agent_id,
            allowed_actions=allowed_actions,
            allowed_topics=allowed_topics,
            ttl_seconds=ttl_seconds,
            parent_token=parent_token,
        )


        issue_delegation_token_body.additional_properties = d
        return issue_delegation_token_body

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
