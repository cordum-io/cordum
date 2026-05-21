from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import Dict
from typing import Union
import datetime

if TYPE_CHECKING:
    from ..models.velocity_rule_match import VelocityRuleMatch


T = TypeVar("T", bound="VelocityRule")


@_attrs_define
class VelocityRule:
    """
    Attributes:
        id (str):
        name (str):
        match (Union[Unset, VelocityRuleMatch]):
        window (Union[Unset, str]):
        key (Union[Unset, str]):
        threshold (Union[Unset, int]):
        decision (Union[Unset, str]):
        reason (Union[Unset, str]):
        enabled (Union[Unset, bool]):
        created_at (Union[Unset, datetime.datetime]):
        updated_at (Union[Unset, datetime.datetime]):
    """

    id: str
    name: str
    match: Union[Unset, "VelocityRuleMatch"] = UNSET
    window: Union[Unset, str] = UNSET
    key: Union[Unset, str] = UNSET
    threshold: Union[Unset, int] = UNSET
    decision: Union[Unset, str] = UNSET
    reason: Union[Unset, str] = UNSET
    enabled: Union[Unset, bool] = UNSET
    created_at: Union[Unset, datetime.datetime] = UNSET
    updated_at: Union[Unset, datetime.datetime] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.velocity_rule_match import VelocityRuleMatch

        id = self.id

        name = self.name

        match: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.match, Unset):
            match = self.match.to_dict()

        window = self.window

        key = self.key

        threshold = self.threshold

        decision = self.decision

        reason = self.reason

        enabled = self.enabled

        created_at: Union[Unset, str] = UNSET
        if not isinstance(self.created_at, Unset):
            created_at = self.created_at.isoformat()

        updated_at: Union[Unset, str] = UNSET
        if not isinstance(self.updated_at, Unset):
            updated_at = self.updated_at.isoformat()

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "name": name,
            }
        )
        if match is not UNSET:
            field_dict["match"] = match
        if window is not UNSET:
            field_dict["window"] = window
        if key is not UNSET:
            field_dict["key"] = key
        if threshold is not UNSET:
            field_dict["threshold"] = threshold
        if decision is not UNSET:
            field_dict["decision"] = decision
        if reason is not UNSET:
            field_dict["reason"] = reason
        if enabled is not UNSET:
            field_dict["enabled"] = enabled
        if created_at is not UNSET:
            field_dict["created_at"] = created_at
        if updated_at is not UNSET:
            field_dict["updated_at"] = updated_at

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.velocity_rule_match import VelocityRuleMatch

        d = src_dict.copy()
        id = d.pop("id")

        name = d.pop("name")

        _match = d.pop("match", UNSET)
        match: Union[Unset, VelocityRuleMatch]
        if isinstance(_match, Unset):
            match = UNSET
        else:
            match = VelocityRuleMatch.from_dict(_match)

        window = d.pop("window", UNSET)

        key = d.pop("key", UNSET)

        threshold = d.pop("threshold", UNSET)

        decision = d.pop("decision", UNSET)

        reason = d.pop("reason", UNSET)

        enabled = d.pop("enabled", UNSET)

        _created_at = d.pop("created_at", UNSET)
        created_at: Union[Unset, datetime.datetime]
        if isinstance(_created_at, Unset):
            created_at = UNSET
        else:
            created_at = isoparse(_created_at)

        _updated_at = d.pop("updated_at", UNSET)
        updated_at: Union[Unset, datetime.datetime]
        if isinstance(_updated_at, Unset):
            updated_at = UNSET
        else:
            updated_at = isoparse(_updated_at)

        velocity_rule = cls(
            id=id,
            name=name,
            match=match,
            window=window,
            key=key,
            threshold=threshold,
            decision=decision,
            reason=reason,
            enabled=enabled,
            created_at=created_at,
            updated_at=updated_at,
        )

        velocity_rule.additional_properties = d
        return velocity_rule

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
