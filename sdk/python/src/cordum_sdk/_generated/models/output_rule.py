from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.output_rule_action import OutputRuleAction
from ..types import UNSET, Unset
from typing import cast
from typing import cast, Union
from typing import Dict
from typing import Union

if TYPE_CHECKING:
    from ..models.output_rule_config_type_0 import OutputRuleConfigType0


T = TypeVar("T", bound="OutputRule")


@_attrs_define
class OutputRule:
    """
    Attributes:
        id (Union[Unset, str]):
        name (Union[Unset, str]):
        description (Union[Unset, str]):
        enabled (Union[Unset, bool]):
        scanner_type (Union[Unset, str]):
        pattern (Union[None, Unset, str]):
        action (Union[Unset, OutputRuleAction]):
        config (Union['OutputRuleConfigType0', None, Unset]):
    """

    id: Union[Unset, str] = UNSET
    name: Union[Unset, str] = UNSET
    description: Union[Unset, str] = UNSET
    enabled: Union[Unset, bool] = UNSET
    scanner_type: Union[Unset, str] = UNSET
    pattern: Union[None, Unset, str] = UNSET
    action: Union[Unset, OutputRuleAction] = UNSET
    config: Union["OutputRuleConfigType0", None, Unset] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.output_rule_config_type_0 import OutputRuleConfigType0

        id = self.id

        name = self.name

        description = self.description

        enabled = self.enabled

        scanner_type = self.scanner_type

        pattern: Union[None, Unset, str]
        if isinstance(self.pattern, Unset):
            pattern = UNSET
        else:
            pattern = self.pattern

        action: Union[Unset, str] = UNSET
        if not isinstance(self.action, Unset):
            action = self.action.value

        config: Union[Dict[str, Any], None, Unset]
        if isinstance(self.config, Unset):
            config = UNSET
        elif isinstance(self.config, OutputRuleConfigType0):
            config = self.config.to_dict()
        else:
            config = self.config

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
        if scanner_type is not UNSET:
            field_dict["scanner_type"] = scanner_type
        if pattern is not UNSET:
            field_dict["pattern"] = pattern
        if action is not UNSET:
            field_dict["action"] = action
        if config is not UNSET:
            field_dict["config"] = config

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.output_rule_config_type_0 import OutputRuleConfigType0

        d = src_dict.copy()
        id = d.pop("id", UNSET)

        name = d.pop("name", UNSET)

        description = d.pop("description", UNSET)

        enabled = d.pop("enabled", UNSET)

        scanner_type = d.pop("scanner_type", UNSET)

        def _parse_pattern(data: object) -> Union[None, Unset, str]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, str], data)

        pattern = _parse_pattern(d.pop("pattern", UNSET))

        _action = d.pop("action", UNSET)
        action: Union[Unset, OutputRuleAction]
        if isinstance(_action, Unset):
            action = UNSET
        else:
            action = OutputRuleAction(_action)

        def _parse_config(data: object) -> Union["OutputRuleConfigType0", None, Unset]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                config_type_0 = OutputRuleConfigType0.from_dict(data)

                return config_type_0
            except:  # noqa: E722
                pass
            return cast(Union["OutputRuleConfigType0", None, Unset], data)

        config = _parse_config(d.pop("config", UNSET))

        output_rule = cls(
            id=id,
            name=name,
            description=description,
            enabled=enabled,
            scanner_type=scanner_type,
            pattern=pattern,
            action=action,
            config=config,
        )

        output_rule.additional_properties = d
        return output_rule

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
