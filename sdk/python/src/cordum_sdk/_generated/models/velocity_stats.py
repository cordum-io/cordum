from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import cast, List
from typing import Union
import datetime


T = TypeVar("T", bound="VelocityStats")


@_attrs_define
class VelocityStats:
    """
    Attributes:
        id (str):
        hit_count_24h (Union[Unset, int]):
        hit_rate_24h (Union[Unset, float]):
        current_window_count (Union[Unset, int]):
        current_window_max (Union[Unset, int]):
        active_buckets (Union[Unset, int]):
        exceeded_buckets (Union[Unset, int]):
        last_triggered (Union[Unset, datetime.datetime]):
        hourly_hits (Union[Unset, List[int]]):
    """

    id: str
    hit_count_24h: Union[Unset, int] = UNSET
    hit_rate_24h: Union[Unset, float] = UNSET
    current_window_count: Union[Unset, int] = UNSET
    current_window_max: Union[Unset, int] = UNSET
    active_buckets: Union[Unset, int] = UNSET
    exceeded_buckets: Union[Unset, int] = UNSET
    last_triggered: Union[Unset, datetime.datetime] = UNSET
    hourly_hits: Union[Unset, List[int]] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        id = self.id

        hit_count_24h = self.hit_count_24h

        hit_rate_24h = self.hit_rate_24h

        current_window_count = self.current_window_count

        current_window_max = self.current_window_max

        active_buckets = self.active_buckets

        exceeded_buckets = self.exceeded_buckets

        last_triggered: Union[Unset, str] = UNSET
        if not isinstance(self.last_triggered, Unset):
            last_triggered = self.last_triggered.isoformat()

        hourly_hits: Union[Unset, List[int]] = UNSET
        if not isinstance(self.hourly_hits, Unset):
            hourly_hits = self.hourly_hits

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
            }
        )
        if hit_count_24h is not UNSET:
            field_dict["hit_count_24h"] = hit_count_24h
        if hit_rate_24h is not UNSET:
            field_dict["hit_rate_24h"] = hit_rate_24h
        if current_window_count is not UNSET:
            field_dict["current_window_count"] = current_window_count
        if current_window_max is not UNSET:
            field_dict["current_window_max"] = current_window_max
        if active_buckets is not UNSET:
            field_dict["active_buckets"] = active_buckets
        if exceeded_buckets is not UNSET:
            field_dict["exceeded_buckets"] = exceeded_buckets
        if last_triggered is not UNSET:
            field_dict["last_triggered"] = last_triggered
        if hourly_hits is not UNSET:
            field_dict["hourly_hits"] = hourly_hits

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        id = d.pop("id")

        hit_count_24h = d.pop("hit_count_24h", UNSET)

        hit_rate_24h = d.pop("hit_rate_24h", UNSET)

        current_window_count = d.pop("current_window_count", UNSET)

        current_window_max = d.pop("current_window_max", UNSET)

        active_buckets = d.pop("active_buckets", UNSET)

        exceeded_buckets = d.pop("exceeded_buckets", UNSET)

        _last_triggered = d.pop("last_triggered", UNSET)
        last_triggered: Union[Unset, datetime.datetime]
        if isinstance(_last_triggered, Unset):
            last_triggered = UNSET
        else:
            last_triggered = isoparse(_last_triggered)

        hourly_hits = cast(List[int], d.pop("hourly_hits", UNSET))

        velocity_stats = cls(
            id=id,
            hit_count_24h=hit_count_24h,
            hit_rate_24h=hit_rate_24h,
            current_window_count=current_window_count,
            current_window_max=current_window_max,
            active_buckets=active_buckets,
            exceeded_buckets=exceeded_buckets,
            last_triggered=last_triggered,
            hourly_hits=hourly_hits,
        )

        velocity_stats.additional_properties = d
        return velocity_stats

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
