from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import Union


T = TypeVar("T", bound="EdgeExecutionMetrics")


@_attrs_define
class EdgeExecutionMetrics:
    """
    Attributes:
        events (Union[Unset, int]):
        allow (Union[Unset, int]):
        deny (Union[Unset, int]):
        require_approval (Union[Unset, int]):
        artifacts (Union[Unset, int]):
        llm_cost_usd (Union[Unset, float]):
    """

    events: Union[Unset, int] = UNSET
    allow: Union[Unset, int] = UNSET
    deny: Union[Unset, int] = UNSET
    require_approval: Union[Unset, int] = UNSET
    artifacts: Union[Unset, int] = UNSET
    llm_cost_usd: Union[Unset, float] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        events = self.events

        allow = self.allow

        deny = self.deny

        require_approval = self.require_approval

        artifacts = self.artifacts

        llm_cost_usd = self.llm_cost_usd

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if events is not UNSET:
            field_dict["events"] = events
        if allow is not UNSET:
            field_dict["allow"] = allow
        if deny is not UNSET:
            field_dict["deny"] = deny
        if require_approval is not UNSET:
            field_dict["require_approval"] = require_approval
        if artifacts is not UNSET:
            field_dict["artifacts"] = artifacts
        if llm_cost_usd is not UNSET:
            field_dict["llm_cost_usd"] = llm_cost_usd

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        events = d.pop("events", UNSET)

        allow = d.pop("allow", UNSET)

        deny = d.pop("deny", UNSET)

        require_approval = d.pop("require_approval", UNSET)

        artifacts = d.pop("artifacts", UNSET)

        llm_cost_usd = d.pop("llm_cost_usd", UNSET)

        edge_execution_metrics = cls(
            events=events,
            allow=allow,
            deny=deny,
            require_approval=require_approval,
            artifacts=artifacts,
            llm_cost_usd=llm_cost_usd,
        )

        edge_execution_metrics.additional_properties = d
        return edge_execution_metrics

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
