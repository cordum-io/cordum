from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import cast, List
from typing import Dict
from typing import Union
import datetime

if TYPE_CHECKING:
    from ..models.generic_object import GenericObject
    from ..models.velocity_stats import VelocityStats


T = TypeVar("T", bound="GetVelocityRuleStatsResponse200")


@_attrs_define
class GetVelocityRuleStatsResponse200:
    """
    Attributes:
        items (List['VelocityStats']):
        top_rules (List['VelocityStats']):
        generated_at (datetime.datetime):
        errors (Union[Unset, List['GenericObject']]):
    """

    items: List["VelocityStats"]
    top_rules: List["VelocityStats"]
    generated_at: datetime.datetime
    errors: Union[Unset, List["GenericObject"]] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.generic_object import GenericObject
        from ..models.velocity_stats import VelocityStats

        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        top_rules = []
        for top_rules_item_data in self.top_rules:
            top_rules_item = top_rules_item_data.to_dict()
            top_rules.append(top_rules_item)

        generated_at = self.generated_at.isoformat()

        errors: Union[Unset, List[Dict[str, Any]]] = UNSET
        if not isinstance(self.errors, Unset):
            errors = []
            for errors_item_data in self.errors:
                errors_item = errors_item_data.to_dict()
                errors.append(errors_item)

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "items": items,
                "top_rules": top_rules,
                "generated_at": generated_at,
            }
        )
        if errors is not UNSET:
            field_dict["errors"] = errors

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.generic_object import GenericObject
        from ..models.velocity_stats import VelocityStats

        d = src_dict.copy()
        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = VelocityStats.from_dict(items_item_data)

            items.append(items_item)

        top_rules = []
        _top_rules = d.pop("top_rules")
        for top_rules_item_data in _top_rules:
            top_rules_item = VelocityStats.from_dict(top_rules_item_data)

            top_rules.append(top_rules_item)

        generated_at = isoparse(d.pop("generated_at"))

        errors = []
        _errors = d.pop("errors", UNSET)
        for errors_item_data in _errors or []:
            errors_item = GenericObject.from_dict(errors_item_data)

            errors.append(errors_item)

        get_velocity_rule_stats_response_200 = cls(
            items=items,
            top_rules=top_rules,
            generated_at=generated_at,
            errors=errors,
        )

        get_velocity_rule_stats_response_200.additional_properties = d
        return get_velocity_rule_stats_response_200

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
