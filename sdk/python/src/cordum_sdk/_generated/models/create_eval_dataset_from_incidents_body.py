from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast, List
from typing import Union






T = TypeVar("T", bound="CreateEvalDatasetFromIncidentsBody")


@_attrs_define
class CreateEvalDatasetFromIncidentsBody:
    """ 
        Attributes:
            name (str):
            tenant (Union[Unset, str]):
            since (Union[Unset, str]):
            until (Union[Unset, str]):
            topic (Union[Unset, str]):
            rule_id (Union[Unset, str]):
            verdicts (Union[Unset, List[str]]):
            agent_id (Union[Unset, str]):
            max_entries (Union[Unset, int]):
            description (Union[Unset, str]):
            dry_run (Union[Unset, bool]):
     """

    name: str
    tenant: Union[Unset, str] = UNSET
    since: Union[Unset, str] = UNSET
    until: Union[Unset, str] = UNSET
    topic: Union[Unset, str] = UNSET
    rule_id: Union[Unset, str] = UNSET
    verdicts: Union[Unset, List[str]] = UNSET
    agent_id: Union[Unset, str] = UNSET
    max_entries: Union[Unset, int] = UNSET
    description: Union[Unset, str] = UNSET
    dry_run: Union[Unset, bool] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)


    def to_dict(self) -> Dict[str, Any]:
        name = self.name

        tenant = self.tenant

        since = self.since

        until = self.until

        topic = self.topic

        rule_id = self.rule_id

        verdicts: Union[Unset, List[str]] = UNSET
        if not isinstance(self.verdicts, Unset):
            verdicts = self.verdicts



        agent_id = self.agent_id

        max_entries = self.max_entries

        description = self.description

        dry_run = self.dry_run


        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
            "name": name,
        })
        if tenant is not UNSET:
            field_dict["tenant"] = tenant
        if since is not UNSET:
            field_dict["since"] = since
        if until is not UNSET:
            field_dict["until"] = until
        if topic is not UNSET:
            field_dict["topic"] = topic
        if rule_id is not UNSET:
            field_dict["rule_id"] = rule_id
        if verdicts is not UNSET:
            field_dict["verdicts"] = verdicts
        if agent_id is not UNSET:
            field_dict["agent_id"] = agent_id
        if max_entries is not UNSET:
            field_dict["max_entries"] = max_entries
        if description is not UNSET:
            field_dict["description"] = description
        if dry_run is not UNSET:
            field_dict["dry_run"] = dry_run

        return field_dict



    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        name = d.pop("name")

        tenant = d.pop("tenant", UNSET)

        since = d.pop("since", UNSET)

        until = d.pop("until", UNSET)

        topic = d.pop("topic", UNSET)

        rule_id = d.pop("rule_id", UNSET)

        verdicts = cast(List[str], d.pop("verdicts", UNSET))


        agent_id = d.pop("agent_id", UNSET)

        max_entries = d.pop("max_entries", UNSET)

        description = d.pop("description", UNSET)

        dry_run = d.pop("dry_run", UNSET)

        create_eval_dataset_from_incidents_body = cls(
            name=name,
            tenant=tenant,
            since=since,
            until=until,
            topic=topic,
            rule_id=rule_id,
            verdicts=verdicts,
            agent_id=agent_id,
            max_entries=max_entries,
            description=description,
            dry_run=dry_run,
        )


        create_eval_dataset_from_incidents_body.additional_properties = d
        return create_eval_dataset_from_incidents_body

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
