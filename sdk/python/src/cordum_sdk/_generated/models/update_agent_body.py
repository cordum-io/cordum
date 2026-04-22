from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast, List
from typing import Union






T = TypeVar("T", bound="UpdateAgentBody")


@_attrs_define
class UpdateAgentBody:
    """ 
        Attributes:
            name (Union[Unset, str]):
            description (Union[Unset, str]):
            owner (Union[Unset, str]):
            team (Union[Unset, str]):
            risk_tier (Union[Unset, str]):
            status (Union[Unset, str]):
            allowed_topics (Union[Unset, List[str]]):
            allowed_pools (Union[Unset, List[str]]):
            allowed_tools (Union[Unset, List[str]]):
            data_classifications (Union[Unset, List[str]]):
     """

    name: Union[Unset, str] = UNSET
    description: Union[Unset, str] = UNSET
    owner: Union[Unset, str] = UNSET
    team: Union[Unset, str] = UNSET
    risk_tier: Union[Unset, str] = UNSET
    status: Union[Unset, str] = UNSET
    allowed_topics: Union[Unset, List[str]] = UNSET
    allowed_pools: Union[Unset, List[str]] = UNSET
    allowed_tools: Union[Unset, List[str]] = UNSET
    data_classifications: Union[Unset, List[str]] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)


    def to_dict(self) -> Dict[str, Any]:
        name = self.name

        description = self.description

        owner = self.owner

        team = self.team

        risk_tier = self.risk_tier

        status = self.status

        allowed_topics: Union[Unset, List[str]] = UNSET
        if not isinstance(self.allowed_topics, Unset):
            allowed_topics = self.allowed_topics



        allowed_pools: Union[Unset, List[str]] = UNSET
        if not isinstance(self.allowed_pools, Unset):
            allowed_pools = self.allowed_pools



        allowed_tools: Union[Unset, List[str]] = UNSET
        if not isinstance(self.allowed_tools, Unset):
            allowed_tools = self.allowed_tools



        data_classifications: Union[Unset, List[str]] = UNSET
        if not isinstance(self.data_classifications, Unset):
            data_classifications = self.data_classifications




        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
        })
        if name is not UNSET:
            field_dict["name"] = name
        if description is not UNSET:
            field_dict["description"] = description
        if owner is not UNSET:
            field_dict["owner"] = owner
        if team is not UNSET:
            field_dict["team"] = team
        if risk_tier is not UNSET:
            field_dict["risk_tier"] = risk_tier
        if status is not UNSET:
            field_dict["status"] = status
        if allowed_topics is not UNSET:
            field_dict["allowed_topics"] = allowed_topics
        if allowed_pools is not UNSET:
            field_dict["allowed_pools"] = allowed_pools
        if allowed_tools is not UNSET:
            field_dict["allowed_tools"] = allowed_tools
        if data_classifications is not UNSET:
            field_dict["data_classifications"] = data_classifications

        return field_dict



    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        name = d.pop("name", UNSET)

        description = d.pop("description", UNSET)

        owner = d.pop("owner", UNSET)

        team = d.pop("team", UNSET)

        risk_tier = d.pop("risk_tier", UNSET)

        status = d.pop("status", UNSET)

        allowed_topics = cast(List[str], d.pop("allowed_topics", UNSET))


        allowed_pools = cast(List[str], d.pop("allowed_pools", UNSET))


        allowed_tools = cast(List[str], d.pop("allowed_tools", UNSET))


        data_classifications = cast(List[str], d.pop("data_classifications", UNSET))


        update_agent_body = cls(
            name=name,
            description=description,
            owner=owner,
            team=team,
            risk_tier=risk_tier,
            status=status,
            allowed_topics=allowed_topics,
            allowed_pools=allowed_pools,
            allowed_tools=allowed_tools,
            data_classifications=data_classifications,
        )


        update_agent_body.additional_properties = d
        return update_agent_body

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
