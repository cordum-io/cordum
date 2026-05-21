from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import Union


T = TypeVar("T", bound="PoolListItem")


@_attrs_define
class PoolListItem:
    """
    Attributes:
        name (str):
        workers (Union[Unset, int]):
        active_jobs (Union[Unset, int]):
        capacity (Union[Unset, int]):
        utilization (Union[Unset, float]):
    """

    name: str
    workers: Union[Unset, int] = UNSET
    active_jobs: Union[Unset, int] = UNSET
    capacity: Union[Unset, int] = UNSET
    utilization: Union[Unset, float] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        name = self.name

        workers = self.workers

        active_jobs = self.active_jobs

        capacity = self.capacity

        utilization = self.utilization

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
            }
        )
        if workers is not UNSET:
            field_dict["workers"] = workers
        if active_jobs is not UNSET:
            field_dict["active_jobs"] = active_jobs
        if capacity is not UNSET:
            field_dict["capacity"] = capacity
        if utilization is not UNSET:
            field_dict["utilization"] = utilization

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        name = d.pop("name")

        workers = d.pop("workers", UNSET)

        active_jobs = d.pop("active_jobs", UNSET)

        capacity = d.pop("capacity", UNSET)

        utilization = d.pop("utilization", UNSET)

        pool_list_item = cls(
            name=name,
            workers=workers,
            active_jobs=active_jobs,
            capacity=capacity,
            utilization=utilization,
        )

        pool_list_item.additional_properties = d
        return pool_list_item

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
