from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast, List
from typing import Union


T = TypeVar("T", bound="CreateAgentBody")


@_attrs_define
class CreateAgentBody:
    """
    Attributes:
        name (str):
        owner (str):
        risk_tier (str):
        agent_id (Union[Unset, str]): Worker id (a principal or API key id) to link to the new identity. A worker
            already linked to an agent identity is refused with 409 AGENT_WORKER_CONFLICT.
        description (Union[Unset, str]):
        team (Union[Unset, str]):
        allowed_topics (Union[Unset, List[str]]):
        allowed_pools (Union[Unset, List[str]]):
        allowed_servers (Union[Unset, List[str]]): MCP server-name glob allowlist. Empty or omitted fail-closes server-
            guarded MCP actions.
        allowed_tools (Union[Unset, List[str]]):
        allowed_resources (Union[Unset, List[str]]): cordum:// resource URI glob allowlist. Empty or omitted fail-closes
            resource-guarded MCP actions.
        entitlements (Union[Unset, List[str]]): Capability tokens the identity holds for MCP required-entitlement
            checks.
        preapproved_mutating_tools (Union[Unset, List[str]]): Mutating MCP tool names/globs this identity may call
            without human approval. Empty or omitted requires approval for every mutating tool.
        data_classifications (Union[Unset, List[str]]):
    """

    name: str
    owner: str
    risk_tier: str
    agent_id: Union[Unset, str] = UNSET
    description: Union[Unset, str] = UNSET
    team: Union[Unset, str] = UNSET
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
        name = self.name

        owner = self.owner

        risk_tier = self.risk_tier

        agent_id = self.agent_id

        description = self.description

        team = self.team

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
        field_dict.update(
            {
                "name": name,
                "owner": owner,
                "risk_tier": risk_tier,
            }
        )
        if agent_id is not UNSET:
            field_dict["agent_id"] = agent_id
        if description is not UNSET:
            field_dict["description"] = description
        if team is not UNSET:
            field_dict["team"] = team
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
        name = d.pop("name")

        owner = d.pop("owner")

        risk_tier = d.pop("risk_tier")

        agent_id = d.pop("agent_id", UNSET)

        description = d.pop("description", UNSET)

        team = d.pop("team", UNSET)

        allowed_topics = cast(List[str], d.pop("allowed_topics", UNSET))

        allowed_pools = cast(List[str], d.pop("allowed_pools", UNSET))

        allowed_servers = cast(List[str], d.pop("allowed_servers", UNSET))

        allowed_tools = cast(List[str], d.pop("allowed_tools", UNSET))

        allowed_resources = cast(List[str], d.pop("allowed_resources", UNSET))

        entitlements = cast(List[str], d.pop("entitlements", UNSET))

        preapproved_mutating_tools = cast(List[str], d.pop("preapproved_mutating_tools", UNSET))

        data_classifications = cast(List[str], d.pop("data_classifications", UNSET))

        create_agent_body = cls(
            name=name,
            owner=owner,
            risk_tier=risk_tier,
            agent_id=agent_id,
            description=description,
            team=team,
            allowed_topics=allowed_topics,
            allowed_pools=allowed_pools,
            allowed_servers=allowed_servers,
            allowed_tools=allowed_tools,
            allowed_resources=allowed_resources,
            entitlements=entitlements,
            preapproved_mutating_tools=preapproved_mutating_tools,
            data_classifications=data_classifications,
        )

        create_agent_body.additional_properties = d
        return create_agent_body

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
