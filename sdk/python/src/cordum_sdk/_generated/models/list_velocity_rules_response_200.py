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
    from ..models.velocity_rule import VelocityRule


T = TypeVar("T", bound="ListVelocityRulesResponse200")


@_attrs_define
class ListVelocityRulesResponse200:
    """
    Attributes:
        items (List['VelocityRule']):
        count (int):
        limit (int):
        updated_at (Union[Unset, datetime.datetime]):
        upgrade_url (Union[Unset, str]):
        errors (Union[Unset, List['GenericObject']]):
    """

    items: List["VelocityRule"]
    count: int
    limit: int
    updated_at: Union[Unset, datetime.datetime] = UNSET
    upgrade_url: Union[Unset, str] = UNSET
    errors: Union[Unset, List["GenericObject"]] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.generic_object import GenericObject
        from ..models.velocity_rule import VelocityRule

        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        count = self.count

        limit = self.limit

        updated_at: Union[Unset, str] = UNSET
        if not isinstance(self.updated_at, Unset):
            updated_at = self.updated_at.isoformat()

        upgrade_url = self.upgrade_url

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
                "count": count,
                "limit": limit,
            }
        )
        if updated_at is not UNSET:
            field_dict["updated_at"] = updated_at
        if upgrade_url is not UNSET:
            field_dict["upgrade_url"] = upgrade_url
        if errors is not UNSET:
            field_dict["errors"] = errors

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.generic_object import GenericObject
        from ..models.velocity_rule import VelocityRule

        d = src_dict.copy()
        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = VelocityRule.from_dict(items_item_data)

            items.append(items_item)

        count = d.pop("count")

        limit = d.pop("limit")

        _updated_at = d.pop("updated_at", UNSET)
        updated_at: Union[Unset, datetime.datetime]
        if isinstance(_updated_at, Unset):
            updated_at = UNSET
        else:
            updated_at = isoparse(_updated_at)

        upgrade_url = d.pop("upgrade_url", UNSET)

        errors = []
        _errors = d.pop("errors", UNSET)
        for errors_item_data in _errors or []:
            errors_item = GenericObject.from_dict(errors_item_data)

            errors.append(errors_item)

        list_velocity_rules_response_200 = cls(
            items=items,
            count=count,
            limit=limit,
            updated_at=updated_at,
            upgrade_url=upgrade_url,
            errors=errors,
        )

        list_velocity_rules_response_200.additional_properties = d
        return list_velocity_rules_response_200

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
