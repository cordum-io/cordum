from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast, Union
from typing import Union






T = TypeVar("T", bound="ApprovalAnalyticsGroup")


@_attrs_define
class ApprovalAnalyticsGroup:
    """ 
        Attributes:
            key (str): Group identifier (rule id, agent id, or topic string).
            label (str):
            total (int):
            approved (int):
            rejected (int):
            expired (int):
            auto_count (int):
            manual_count (int):
            avg_ttar_seconds (Union[None, Unset, float]):
            p90_seconds (Union[None, Unset, float]):
     """

    key: str
    label: str
    total: int
    approved: int
    rejected: int
    expired: int
    auto_count: int
    manual_count: int
    avg_ttar_seconds: Union[None, Unset, float] = UNSET
    p90_seconds: Union[None, Unset, float] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)


    def to_dict(self) -> Dict[str, Any]:
        key = self.key

        label = self.label

        total = self.total

        approved = self.approved

        rejected = self.rejected

        expired = self.expired

        auto_count = self.auto_count

        manual_count = self.manual_count

        avg_ttar_seconds: Union[None, Unset, float]
        if isinstance(self.avg_ttar_seconds, Unset):
            avg_ttar_seconds = UNSET
        else:
            avg_ttar_seconds = self.avg_ttar_seconds

        p90_seconds: Union[None, Unset, float]
        if isinstance(self.p90_seconds, Unset):
            p90_seconds = UNSET
        else:
            p90_seconds = self.p90_seconds


        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
            "key": key,
            "label": label,
            "total": total,
            "approved": approved,
            "rejected": rejected,
            "expired": expired,
            "auto_count": auto_count,
            "manual_count": manual_count,
        })
        if avg_ttar_seconds is not UNSET:
            field_dict["avg_ttar_seconds"] = avg_ttar_seconds
        if p90_seconds is not UNSET:
            field_dict["p90_seconds"] = p90_seconds

        return field_dict



    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        key = d.pop("key")

        label = d.pop("label")

        total = d.pop("total")

        approved = d.pop("approved")

        rejected = d.pop("rejected")

        expired = d.pop("expired")

        auto_count = d.pop("auto_count")

        manual_count = d.pop("manual_count")

        def _parse_avg_ttar_seconds(data: object) -> Union[None, Unset, float]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, float], data)

        avg_ttar_seconds = _parse_avg_ttar_seconds(d.pop("avg_ttar_seconds", UNSET))


        def _parse_p90_seconds(data: object) -> Union[None, Unset, float]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, float], data)

        p90_seconds = _parse_p90_seconds(d.pop("p90_seconds", UNSET))


        approval_analytics_group = cls(
            key=key,
            label=label,
            total=total,
            approved=approved,
            rejected=rejected,
            expired=expired,
            auto_count=auto_count,
            manual_count=manual_count,
            avg_ttar_seconds=avg_ttar_seconds,
            p90_seconds=p90_seconds,
        )


        approval_analytics_group.additional_properties = d
        return approval_analytics_group

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
