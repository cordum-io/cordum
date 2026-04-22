from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast, List
from typing import Union






T = TypeVar("T", bound="ListAgentsResponse200ItemsItem")


@_attrs_define
class ListAgentsResponse200ItemsItem:
    """ 
        Attributes:
            id (Union[Unset, str]):
            name (Union[Unset, str]):
            description (Union[Unset, str]):
            owner (Union[Unset, str]):
            team (Union[Unset, str]):
            risk_tier (Union[Unset, str]):
            allowed_topics (Union[Unset, List[str]]):
            allowed_pools (Union[Unset, List[str]]):
            allowed_tools (Union[Unset, List[str]]):
            data_classifications (Union[Unset, List[str]]):
            status (Union[Unset, str]):
            created_at (Union[Unset, str]):
            updated_at (Union[Unset, str]):
            last_active (Union[Unset, int]):
     """

    id: Union[Unset, str] = UNSET
    name: Union[Unset, str] = UNSET
    description: Union[Unset, str] = UNSET
    owner: Union[Unset, str] = UNSET
    team: Union[Unset, str] = UNSET
    risk_tier: Union[Unset, str] = UNSET
    allowed_topics: Union[Unset, List[str]] = UNSET
    allowed_pools: Union[Unset, List[str]] = UNSET
    allowed_tools: Union[Unset, List[str]] = UNSET
    data_classifications: Union[Unset, List[str]] = UNSET
    status: Union[Unset, str] = UNSET
    created_at: Union[Unset, str] = UNSET
    updated_at: Union[Unset, str] = UNSET
    last_active: Union[Unset, int] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)


    def to_dict(self) -> Dict[str, Any]:
        id = self.id

        name = self.name

        description = self.description

        owner = self.owner

        team = self.team

        risk_tier = self.risk_tier

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



        status = self.status

        created_at = self.created_at

        updated_at = self.updated_at

        last_active = self.last_active


        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
        })
        if id is not UNSET:
            field_dict["id"] = id
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
        if allowed_topics is not UNSET:
            field_dict["allowed_topics"] = allowed_topics
        if allowed_pools is not UNSET:
            field_dict["allowed_pools"] = allowed_pools
        if allowed_tools is not UNSET:
            field_dict["allowed_tools"] = allowed_tools
        if data_classifications is not UNSET:
            field_dict["data_classifications"] = data_classifications
        if status is not UNSET:
            field_dict["status"] = status
        if created_at is not UNSET:
            field_dict["created_at"] = created_at
        if updated_at is not UNSET:
            field_dict["updated_at"] = updated_at
        if last_active is not UNSET:
            field_dict["last_active"] = last_active

        return field_dict



    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        id = d.pop("id", UNSET)

        name = d.pop("name", UNSET)

        description = d.pop("description", UNSET)

        owner = d.pop("owner", UNSET)

        team = d.pop("team", UNSET)

        risk_tier = d.pop("risk_tier", UNSET)

        allowed_topics = cast(List[str], d.pop("allowed_topics", UNSET))


        allowed_pools = cast(List[str], d.pop("allowed_pools", UNSET))


        allowed_tools = cast(List[str], d.pop("allowed_tools", UNSET))


        data_classifications = cast(List[str], d.pop("data_classifications", UNSET))


        status = d.pop("status", UNSET)

        created_at = d.pop("created_at", UNSET)

        updated_at = d.pop("updated_at", UNSET)

        last_active = d.pop("last_active", UNSET)

        list_agents_response_200_items_item = cls(
            id=id,
            name=name,
            description=description,
            owner=owner,
            team=team,
            risk_tier=risk_tier,
            allowed_topics=allowed_topics,
            allowed_pools=allowed_pools,
            allowed_tools=allowed_tools,
            data_classifications=data_classifications,
            status=status,
            created_at=created_at,
            updated_at=updated_at,
            last_active=last_active,
        )


        list_agents_response_200_items_item.additional_properties = d
        return list_agents_response_200_items_item

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
