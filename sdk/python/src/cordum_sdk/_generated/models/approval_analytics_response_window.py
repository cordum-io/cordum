from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from dateutil.parser import isoparse
from typing import cast
import datetime






T = TypeVar("T", bound="ApprovalAnalyticsResponseWindow")


@_attrs_define
class ApprovalAnalyticsResponseWindow:
    """ 
        Attributes:
            since (datetime.datetime):
            until (datetime.datetime):
     """

    since: datetime.datetime
    until: datetime.datetime
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)


    def to_dict(self) -> Dict[str, Any]:
        since = self.since.isoformat()

        until = self.until.isoformat()


        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
            "since": since,
            "until": until,
        })

        return field_dict



    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        since = isoparse(d.pop("since"))




        until = isoparse(d.pop("until"))




        approval_analytics_response_window = cls(
            since=since,
            until=until,
        )


        approval_analytics_response_window.additional_properties = d
        return approval_analytics_response_window

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
