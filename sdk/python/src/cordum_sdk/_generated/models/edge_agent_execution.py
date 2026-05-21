from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.edge_agent_execution_adapter import EdgeAgentExecutionAdapter
from ..models.edge_agent_execution_mode import EdgeAgentExecutionMode
from ..models.edge_agent_execution_status import EdgeAgentExecutionStatus
from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import cast, Union
from typing import Dict
from typing import Union
import datetime

if TYPE_CHECKING:
    from ..models.edge_execution_metrics import EdgeExecutionMetrics
    from ..models.edge_labels import EdgeLabels


T = TypeVar("T", bound="EdgeAgentExecution")


@_attrs_define
class EdgeAgentExecution:
    """
    Attributes:
        execution_id (str):
        session_id (str):
        tenant_id (str):
        adapter (EdgeAgentExecutionAdapter):
        mode (EdgeAgentExecutionMode):
        status (EdgeAgentExecutionStatus):
        started_at (datetime.datetime):
        workflow_run_id (Union[Unset, str]):
        step_id (Union[Unset, str]):
        job_id (Union[Unset, str]):
        attempt (Union[Unset, int]):
        trace_id (Union[Unset, str]):
        worker_id (Union[Unset, str]):
        policy_snapshot (Union[Unset, str]):
        ended_at (Union[None, Unset, datetime.datetime]):
        metrics (Union[Unset, EdgeExecutionMetrics]):
        labels (Union[Unset, EdgeLabels]):
    """

    execution_id: str
    session_id: str
    tenant_id: str
    adapter: EdgeAgentExecutionAdapter
    mode: EdgeAgentExecutionMode
    status: EdgeAgentExecutionStatus
    started_at: datetime.datetime
    workflow_run_id: Union[Unset, str] = UNSET
    step_id: Union[Unset, str] = UNSET
    job_id: Union[Unset, str] = UNSET
    attempt: Union[Unset, int] = UNSET
    trace_id: Union[Unset, str] = UNSET
    worker_id: Union[Unset, str] = UNSET
    policy_snapshot: Union[Unset, str] = UNSET
    ended_at: Union[None, Unset, datetime.datetime] = UNSET
    metrics: Union[Unset, "EdgeExecutionMetrics"] = UNSET
    labels: Union[Unset, "EdgeLabels"] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.edge_execution_metrics import EdgeExecutionMetrics
        from ..models.edge_labels import EdgeLabels

        execution_id = self.execution_id

        session_id = self.session_id

        tenant_id = self.tenant_id

        adapter = self.adapter.value

        mode = self.mode.value

        status = self.status.value

        started_at = self.started_at.isoformat()

        workflow_run_id = self.workflow_run_id

        step_id = self.step_id

        job_id = self.job_id

        attempt = self.attempt

        trace_id = self.trace_id

        worker_id = self.worker_id

        policy_snapshot = self.policy_snapshot

        ended_at: Union[None, Unset, str]
        if isinstance(self.ended_at, Unset):
            ended_at = UNSET
        elif isinstance(self.ended_at, datetime.datetime):
            ended_at = self.ended_at.isoformat()
        else:
            ended_at = self.ended_at

        metrics: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.metrics, Unset):
            metrics = self.metrics.to_dict()

        labels: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.labels, Unset):
            labels = self.labels.to_dict()

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "execution_id": execution_id,
                "session_id": session_id,
                "tenant_id": tenant_id,
                "adapter": adapter,
                "mode": mode,
                "status": status,
                "started_at": started_at,
            }
        )
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
        if ended_at is not UNSET:
            field_dict["ended_at"] = ended_at
        if metrics is not UNSET:
            field_dict["metrics"] = metrics
        if labels is not UNSET:
            field_dict["labels"] = labels

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.edge_execution_metrics import EdgeExecutionMetrics
        from ..models.edge_labels import EdgeLabels

        d = src_dict.copy()
        execution_id = d.pop("execution_id")

        session_id = d.pop("session_id")

        tenant_id = d.pop("tenant_id")

        adapter = EdgeAgentExecutionAdapter(d.pop("adapter"))

        mode = EdgeAgentExecutionMode(d.pop("mode"))

        status = EdgeAgentExecutionStatus(d.pop("status"))

        started_at = isoparse(d.pop("started_at"))

        workflow_run_id = d.pop("workflow_run_id", UNSET)

        step_id = d.pop("step_id", UNSET)

        job_id = d.pop("job_id", UNSET)

        attempt = d.pop("attempt", UNSET)

        trace_id = d.pop("trace_id", UNSET)

        worker_id = d.pop("worker_id", UNSET)

        policy_snapshot = d.pop("policy_snapshot", UNSET)

        def _parse_ended_at(data: object) -> Union[None, Unset, datetime.datetime]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                ended_at_type_0 = isoparse(data)

                return ended_at_type_0
            except:  # noqa: E722
                pass
            return cast(Union[None, Unset, datetime.datetime], data)

        ended_at = _parse_ended_at(d.pop("ended_at", UNSET))

        _metrics = d.pop("metrics", UNSET)
        metrics: Union[Unset, EdgeExecutionMetrics]
        if isinstance(_metrics, Unset):
            metrics = UNSET
        else:
            metrics = EdgeExecutionMetrics.from_dict(_metrics)

        _labels = d.pop("labels", UNSET)
        labels: Union[Unset, EdgeLabels]
        if isinstance(_labels, Unset):
            labels = UNSET
        else:
            labels = EdgeLabels.from_dict(_labels)

        edge_agent_execution = cls(
            execution_id=execution_id,
            session_id=session_id,
            tenant_id=tenant_id,
            adapter=adapter,
            mode=mode,
            status=status,
            started_at=started_at,
            workflow_run_id=workflow_run_id,
            step_id=step_id,
            job_id=job_id,
            attempt=attempt,
            trace_id=trace_id,
            worker_id=worker_id,
            policy_snapshot=policy_snapshot,
            ended_at=ended_at,
            metrics=metrics,
            labels=labels,
        )

        edge_agent_execution.additional_properties = d
        return edge_agent_execution

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
