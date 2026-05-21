from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.edge_execution_create_request_adapter import EdgeExecutionCreateRequestAdapter
from ..models.edge_execution_create_request_mode import EdgeExecutionCreateRequestMode
from ..types import UNSET, Unset
from typing import cast
from typing import Dict
from typing import Union

if TYPE_CHECKING:
    from ..models.edge_labels import EdgeLabels


T = TypeVar("T", bound="EdgeExecutionCreateRequest")


@_attrs_define
class EdgeExecutionCreateRequest:
    """
    Attributes:
        session_id (str):
        tenant_id (Union[Unset, str]): Optional body tenant; when present it must match X-Tenant-ID.
        adapter (Union[Unset, EdgeExecutionCreateRequestAdapter]):
        mode (Union[Unset, EdgeExecutionCreateRequestMode]):
        workflow_run_id (Union[Unset, str]):
        step_id (Union[Unset, str]):
        job_id (Union[Unset, str]):
        attempt (Union[Unset, int]):
        trace_id (Union[Unset, str]):
        worker_id (Union[Unset, str]):
        policy_snapshot (Union[Unset, str]): Redacted policy snapshot identifier or summary; raw secrets are redacted
            before persistence/response.
        labels (Union[Unset, EdgeLabels]):
    """

    session_id: str
    tenant_id: Union[Unset, str] = UNSET
    adapter: Union[Unset, EdgeExecutionCreateRequestAdapter] = UNSET
    mode: Union[Unset, EdgeExecutionCreateRequestMode] = UNSET
    workflow_run_id: Union[Unset, str] = UNSET
    step_id: Union[Unset, str] = UNSET
    job_id: Union[Unset, str] = UNSET
    attempt: Union[Unset, int] = UNSET
    trace_id: Union[Unset, str] = UNSET
    worker_id: Union[Unset, str] = UNSET
    policy_snapshot: Union[Unset, str] = UNSET
    labels: Union[Unset, "EdgeLabels"] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.edge_labels import EdgeLabels

        session_id = self.session_id

        tenant_id = self.tenant_id

        adapter: Union[Unset, str] = UNSET
        if not isinstance(self.adapter, Unset):
            adapter = self.adapter.value

        mode: Union[Unset, str] = UNSET
        if not isinstance(self.mode, Unset):
            mode = self.mode.value

        workflow_run_id = self.workflow_run_id

        step_id = self.step_id

        job_id = self.job_id

        attempt = self.attempt

        trace_id = self.trace_id

        worker_id = self.worker_id

        policy_snapshot = self.policy_snapshot

        labels: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.labels, Unset):
            labels = self.labels.to_dict()

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "session_id": session_id,
            }
        )
        if tenant_id is not UNSET:
            field_dict["tenant_id"] = tenant_id
        if adapter is not UNSET:
            field_dict["adapter"] = adapter
        if mode is not UNSET:
            field_dict["mode"] = mode
        if workflow_run_id is not UNSET:
            field_dict["workflow_run_id"] = workflow_run_id
        if step_id is not UNSET:
            field_dict["step_id"] = step_id
        if job_id is not UNSET:
            field_dict["job_id"] = job_id
        if attempt is not UNSET:
            field_dict["attempt"] = attempt
        if trace_id is not UNSET:
            field_dict["trace_id"] = trace_id
        if worker_id is not UNSET:
            field_dict["worker_id"] = worker_id
        if policy_snapshot is not UNSET:
            field_dict["policy_snapshot"] = policy_snapshot
        if labels is not UNSET:
            field_dict["labels"] = labels

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.edge_labels import EdgeLabels

        d = src_dict.copy()
        session_id = d.pop("session_id")

        tenant_id = d.pop("tenant_id", UNSET)

        _adapter = d.pop("adapter", UNSET)
        adapter: Union[Unset, EdgeExecutionCreateRequestAdapter]
        if isinstance(_adapter, Unset):
            adapter = UNSET
        else:
            adapter = EdgeExecutionCreateRequestAdapter(_adapter)

        _mode = d.pop("mode", UNSET)
        mode: Union[Unset, EdgeExecutionCreateRequestMode]
        if isinstance(_mode, Unset):
            mode = UNSET
        else:
            mode = EdgeExecutionCreateRequestMode(_mode)

        workflow_run_id = d.pop("workflow_run_id", UNSET)

        step_id = d.pop("step_id", UNSET)

        job_id = d.pop("job_id", UNSET)

        attempt = d.pop("attempt", UNSET)

        trace_id = d.pop("trace_id", UNSET)

        worker_id = d.pop("worker_id", UNSET)

        policy_snapshot = d.pop("policy_snapshot", UNSET)

        _labels = d.pop("labels", UNSET)
        labels: Union[Unset, EdgeLabels]
        if isinstance(_labels, Unset):
            labels = UNSET
        else:
            labels = EdgeLabels.from_dict(_labels)

        edge_execution_create_request = cls(
            session_id=session_id,
            tenant_id=tenant_id,
            adapter=adapter,
            mode=mode,
            workflow_run_id=workflow_run_id,
            step_id=step_id,
            job_id=job_id,
            attempt=attempt,
            trace_id=trace_id,
            worker_id=worker_id,
            policy_snapshot=policy_snapshot,
            labels=labels,
        )

        edge_execution_create_request.additional_properties = d
        return edge_execution_create_request

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
