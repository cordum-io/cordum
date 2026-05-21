from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import Union
import datetime


T = TypeVar("T", bound="PolicyAnalyticsBody")


@_attrs_define
class PolicyAnalyticsBody:
    """
    Attributes:
        from_ (datetime.datetime): Start of analysis window (RFC3339)
        to (datetime.datetime): End of analysis window (RFC3339, max 7 days from 'from')
        rule_filter (Union[Unset, str]): Optional rule ID to restrict analysis to a single rule
    """

    from_: datetime.datetime
    to: datetime.datetime
    rule_filter: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from_ = self.from_.isoformat()

        to = self.to.isoformat()

        rule_filter = self.rule_filter

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "from": from_,
                "to": to,
            }
        )
        if rule_filter is not UNSET:
            field_dict["rule_filter"] = rule_filter

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        from_ = isoparse(d.pop("from"))

        to = isoparse(d.pop("to"))

        rule_filter = d.pop("rule_filter", UNSET)

        policy_analytics_body = cls(
            from_=from_,
            to=to,
            rule_filter=rule_filter,
        )

        policy_analytics_body.additional_properties = d
        return policy_analytics_body

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
