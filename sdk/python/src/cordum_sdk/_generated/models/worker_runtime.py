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
    from ..models.worker_runtime_labels import WorkerRuntimeLabels


T = TypeVar("T", bound="WorkerRuntime")


@_attrs_define
class WorkerRuntime:
    """
    Attributes:
        worker_id (str):
        pool (Union[Unset, str]):
        active_jobs (Union[Unset, int]):
        max_parallel_jobs (Union[Unset, int]):
        capabilities (Union[Unset, List[str]]):
        cpu_load (Union[Unset, float]):
        gpu_utilization (Union[Unset, float]):
        memory_load (Union[Unset, float]):
        region (Union[Unset, str]):
        type (Union[Unset, str]):
        labels (Union[Unset, WorkerRuntimeLabels]):
        last_heartbeat (Union[Unset, datetime.datetime]):
    """

    worker_id: str
    pool: Union[Unset, str] = UNSET
    active_jobs: Union[Unset, int] = UNSET
    max_parallel_jobs: Union[Unset, int] = UNSET
    capabilities: Union[Unset, List[str]] = UNSET
    cpu_load: Union[Unset, float] = UNSET
    gpu_utilization: Union[Unset, float] = UNSET
    memory_load: Union[Unset, float] = UNSET
    region: Union[Unset, str] = UNSET
    type: Union[Unset, str] = UNSET
    labels: Union[Unset, "WorkerRuntimeLabels"] = UNSET
    last_heartbeat: Union[Unset, datetime.datetime] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.worker_runtime_labels import WorkerRuntimeLabels

        worker_id = self.worker_id

        pool = self.pool

        active_jobs = self.active_jobs

        max_parallel_jobs = self.max_parallel_jobs

        capabilities: Union[Unset, List[str]] = UNSET
        if not isinstance(self.capabilities, Unset):
            capabilities = self.capabilities

        cpu_load = self.cpu_load

        gpu_utilization = self.gpu_utilization

        memory_load = self.memory_load

        region = self.region

        type = self.type

        labels: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.labels, Unset):
            labels = self.labels.to_dict()

        last_heartbeat: Union[Unset, str] = UNSET
        if not isinstance(self.last_heartbeat, Unset):
            last_heartbeat = self.last_heartbeat.isoformat()

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "worker_id": worker_id,
            }
        )
        if pool is not UNSET:
            field_dict["pool"] = pool
        if active_jobs is not UNSET:
            field_dict["active_jobs"] = active_jobs
        if max_parallel_jobs is not UNSET:
            field_dict["max_parallel_jobs"] = max_parallel_jobs
        if capabilities is not UNSET:
            field_dict["capabilities"] = capabilities
        if cpu_load is not UNSET:
            field_dict["cpu_load"] = cpu_load
        if gpu_utilization is not UNSET:
            field_dict["gpu_utilization"] = gpu_utilization
        if memory_load is not UNSET:
            field_dict["memory_load"] = memory_load
        if region is not UNSET:
            field_dict["region"] = region
        if type is not UNSET:
            field_dict["type"] = type
        if labels is not UNSET:
            field_dict["labels"] = labels
        if last_heartbeat is not UNSET:
            field_dict["last_heartbeat"] = last_heartbeat

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.worker_runtime_labels import WorkerRuntimeLabels

        d = src_dict.copy()
        worker_id = d.pop("worker_id")

        pool = d.pop("pool", UNSET)

        active_jobs = d.pop("active_jobs", UNSET)

        max_parallel_jobs = d.pop("max_parallel_jobs", UNSET)

        capabilities = cast(List[str], d.pop("capabilities", UNSET))

        cpu_load = d.pop("cpu_load", UNSET)

        gpu_utilization = d.pop("gpu_utilization", UNSET)

        memory_load = d.pop("memory_load", UNSET)

        region = d.pop("region", UNSET)

        type = d.pop("type", UNSET)

        _labels = d.pop("labels", UNSET)
        labels: Union[Unset, WorkerRuntimeLabels]
        if isinstance(_labels, Unset):
            labels = UNSET
        else:
            labels = WorkerRuntimeLabels.from_dict(_labels)

        _last_heartbeat = d.pop("last_heartbeat", UNSET)
        last_heartbeat: Union[Unset, datetime.datetime]
        if isinstance(_last_heartbeat, Unset):
            last_heartbeat = UNSET
        else:
            last_heartbeat = isoparse(_last_heartbeat)

        worker_runtime = cls(
            worker_id=worker_id,
            pool=pool,
            active_jobs=active_jobs,
            max_parallel_jobs=max_parallel_jobs,
            capabilities=capabilities,
            cpu_load=cpu_load,
            gpu_utilization=gpu_utilization,
            memory_load=memory_load,
            region=region,
            type=type,
            labels=labels,
            last_heartbeat=last_heartbeat,
        )

        worker_runtime.additional_properties = d
        return worker_runtime

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
