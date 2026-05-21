from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast
from typing import cast, List
from typing import Dict
from typing import Union

if TYPE_CHECKING:
    from ..models.worker_runtime import WorkerRuntime


T = TypeVar("T", bound="PoolDetail")


@_attrs_define
class PoolDetail:
    """
    Attributes:
        name (str):
        workers (List['WorkerRuntime']):
        topics (List[str]):
        active_jobs (Union[Unset, int]):
        capacity (Union[Unset, int]):
        utilization (Union[Unset, float]):
    """

    name: str
    workers: List["WorkerRuntime"]
    topics: List[str]
    active_jobs: Union[Unset, int] = UNSET
    capacity: Union[Unset, int] = UNSET
    utilization: Union[Unset, float] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.worker_runtime import WorkerRuntime

        name = self.name

        workers = []
        for workers_item_data in self.workers:
            workers_item = workers_item_data.to_dict()
            workers.append(workers_item)

        topics = self.topics

        active_jobs = self.active_jobs

        capacity = self.capacity

        utilization = self.utilization

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "workers": workers,
                "topics": topics,
            }
        )
        if active_jobs is not UNSET:
            field_dict["active_jobs"] = active_jobs
        if capacity is not UNSET:
            field_dict["capacity"] = capacity
        if utilization is not UNSET:
            field_dict["utilization"] = utilization

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.worker_runtime import WorkerRuntime

        d = src_dict.copy()
        name = d.pop("name")

        workers = []
        _workers = d.pop("workers")
        for workers_item_data in _workers:
            workers_item = WorkerRuntime.from_dict(workers_item_data)

            workers.append(workers_item)

        topics = cast(List[str], d.pop("topics"))

        active_jobs = d.pop("active_jobs", UNSET)

        capacity = d.pop("capacity", UNSET)

        utilization = d.pop("utilization", UNSET)

        pool_detail = cls(
            name=name,
            workers=workers,
            topics=topics,
            active_jobs=active_jobs,
            capacity=capacity,
            utilization=utilization,
        )

        pool_detail.additional_properties = d
        return pool_detail

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
