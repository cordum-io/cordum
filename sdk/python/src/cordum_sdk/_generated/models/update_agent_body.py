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
        agent_id (Union[Unset, str]): Worker id (a principal or API key id) to link to this identity, so an existing
            identity can be bound without being re-created. A worker already linked to another identity is refused with 409
            AGENT_WORKER_CONFLICT.
        name (Union[Unset, str]):
        description (Union[Unset, str]):
        owner (Union[Unset, str]):
        team (Union[Unset, str]):
        risk_tier (Union[Unset, str]):
        status (Union[Unset, str]):
        allowed_topics (Union[Unset, List[str]]):
        allowed_pools (Union[Unset, List[str]]):
        allowed_servers (Union[Unset, List[str]]): Full replacement MCP server-name glob allowlist. Send [] to clear.
        allowed_tools (Union[Unset, List[str]]):
        allowed_resources (Union[Unset, List[str]]): Full replacement cordum:// resource URI glob allowlist. Send [] to
            clear.
        entitlements (Union[Unset, List[str]]): Full replacement capability-token list. Send [] to clear.
        preapproved_mutating_tools (Union[Unset, List[str]]): Full replacement list of mutating MCP tool names/globs
            allowed without human approval. Send [] to clear.
        data_classifications (Union[Unset, List[str]]):
    """

    agent_id: Union[Unset, str] = UNSET
    name: Union[Unset, str] = UNSET
    description: Union[Unset, str] = UNSET
    owner: Union[Unset, str] = UNSET
    team: Union[Unset, str] = UNSET
    risk_tier: Union[Unset, str] = UNSET
    status: Union[Unset, str] = UNSET
    allowed_topics: Union[Unset, List[str]] = UNSET
    allowed_pools: Union[Unset, List[str]] = UNSET
    allowed_servers: Union[Unset, List[str]] = UNSET
    allowed_tools: Union[Unset, List[str]] = UNSET
    allowed_resources: Union[Unset, List[str]] = UNSET
    entitlements: Union[Unset, List[str]] = UNSET
    preapproved_mutating_tools: Union[Unset, List[str]] = UNSET
    data_classifications: Union[Unset, List[str]] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        agent_id = self.agent_id

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

        allowed_servers: Union[Unset, List[str]] = UNSET
        if not isinstance(self.allowed_servers, Unset):
            allowed_servers = self.allowed_servers

        allowed_tools: Union[Unset, List[str]] = UNSET
        if not isinstance(self.allowed_tools, Unset):
            allowed_tools = self.allowed_tools

        allowed_resources: Union[Unset, List[str]] = UNSET
        if not isinstance(self.allowed_resources, Unset):
            allowed_resources = self.allowed_resources

        entitlements: Union[Unset, List[str]] = UNSET
        if not isinstance(self.entitlements, Unset):
            entitlements = self.entitlements

        preapproved_mutating_tools: Union[Unset, List[str]] = UNSET
        if not isinstance(self.preapproved_mutating_tools, Unset):
            preapproved_mutating_tools = self.preapproved_mutating_tools

        data_classifications: Union[Unset, List[str]] = UNSET
        if not isinstance(self.data_classifications, Unset):
            data_classifications = self.data_classifications

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if agent_id is not UNSET:
            field_dict["agent_id"] = agent_id
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
        if allowed_servers is not UNSET:
            field_dict["allowed_servers"] = allowed_servers
        if allowed_tools is not UNSET:
            field_dict["allowed_tools"] = allowed_tools
        if allowed_resources is not UNSET:
            field_dict["allowed_resources"] = allowed_resources
        if entitlements is not UNSET:
            field_dict["entitlements"] = entitlements
        if preapproved_mutating_tools is not UNSET:
            field_dict["preapproved_mutating_tools"] = preapproved_mutating_tools
        if data_classifications is not UNSET:
            field_dict["data_classifications"] = data_classifications

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        agent_id = d.pop("agent_id", UNSET)

        name = d.pop("name", UNSET)

        description = d.pop("description", UNSET)

        owner = d.pop("owner", UNSET)

        team = d.pop("team", UNSET)

        risk_tier = d.pop("risk_tier", UNSET)

        status = d.pop("status", UNSET)

        allowed_topics = cast(List[str], d.pop("allowed_topics", UNSET))

        allowed_pools = cast(List[str], d.pop("allowed_pools", UNSET))

        allowed_servers = cast(List[str], d.pop("allowed_servers", UNSET))

        allowed_tools = cast(List[str], d.pop("allowed_tools", UNSET))

        allowed_resources = cast(List[str], d.pop("allowed_resources", UNSET))

        entitlements = cast(List[str], d.pop("entitlements", UNSET))

        preapproved_mutating_tools = cast(List[str], d.pop("preapproved_mutating_tools", UNSET))

        data_classifications = cast(List[str], d.pop("data_classifications", UNSET))

        update_agent_body = cls(
            agent_id=agent_id,
            name=name,
            description=description,
            owner=owner,
            team=team,
            risk_tier=risk_tier,
            status=status,
            allowed_topics=allowed_topics,
            allowed_pools=allowed_pools,
            allowed_servers=allowed_servers,
            allowed_tools=allowed_tools,
            allowed_resources=allowed_resources,
            entitlements=entitlements,
            preapproved_mutating_tools=preapproved_mutating_tools,
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
