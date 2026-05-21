from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.policy_rule_action import PolicyRuleAction
from ..types import UNSET, Unset
from typing import cast
from typing import Dict
from typing import Union

if TYPE_CHECKING:
    from ..models.policy_rule_conditions import PolicyRuleConditions


T = TypeVar("T", bound="PolicyRule")


@_attrs_define
class PolicyRule:
    """
    Attributes:
        id (Union[Unset, str]):
        name (Union[Unset, str]):
        description (Union[Unset, str]):
        enabled (Union[Unset, bool]):
        action (Union[Unset, PolicyRuleAction]):
        conditions (Union[Unset, PolicyRuleConditions]):
        priority (Union[Unset, int]):
        source (Union[Unset, str]): Bundle or file that defines this rule
    """

    id: Union[Unset, str] = UNSET
    name: Union[Unset, str] = UNSET
    description: Union[Unset, str] = UNSET
    enabled: Union[Unset, bool] = UNSET
    action: Union[Unset, PolicyRuleAction] = UNSET
    conditions: Union[Unset, "PolicyRuleConditions"] = UNSET
    priority: Union[Unset, int] = UNSET
    source: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.policy_rule_conditions import PolicyRuleConditions

        id = self.id

        name = self.name

        description = self.description

        enabled = self.enabled

        action: Union[Unset, str] = UNSET
        if not isinstance(self.action, Unset):
            action = self.action.value

        conditions: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.conditions, Unset):
            conditions = self.conditions.to_dict()

        priority = self.priority

        source = self.source

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if id is not UNSET:
            field_dict["id"] = id
        if name is not UNSET:
            field_dict["name"] = name
        if description is not UNSET:
            field_dict["description"] = description
        if enabled is not UNSET:
            field_dict["enabled"] = enabled
        if action is not UNSET:
            field_dict["action"] = action
        if conditions is not UNSET:
            field_dict["conditions"] = conditions
        if priority is not UNSET:
            field_dict["priority"] = priority
        if source is not UNSET:
            field_dict["source"] = source

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.policy_rule_conditions import PolicyRuleConditions

        d = src_dict.copy()
        id = d.pop("id", UNSET)

        name = d.pop("name", UNSET)

        description = d.pop("description", UNSET)

        enabled = d.pop("enabled", UNSET)

        _action = d.pop("action", UNSET)
        action: Union[Unset, PolicyRuleAction]
        if isinstance(_action, Unset):
            action = UNSET
        else:
            action = PolicyRuleAction(_action)

        _conditions = d.pop("conditions", UNSET)
        conditions: Union[Unset, PolicyRuleConditions]
        if isinstance(_conditions, Unset):
            conditions = UNSET
        else:
            conditions = PolicyRuleConditions.from_dict(_conditions)

        priority = d.pop("priority", UNSET)

        source = d.pop("source", UNSET)

        policy_rule = cls(
            id=id,
            name=name,
            description=description,
            enabled=enabled,
            action=action,
            conditions=conditions,
            priority=priority,
            source=source,
        )

        policy_rule.additional_properties = d
        return policy_rule

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
