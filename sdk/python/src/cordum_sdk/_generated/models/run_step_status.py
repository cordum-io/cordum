from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import cast, Union
from typing import Dict
from typing import Union
import datetime

if TYPE_CHECKING:
    from ..models.run_step_status_output_type_0 import RunStepStatusOutputType0


T = TypeVar("T", bound="RunStepStatus")


@_attrs_define
class RunStepStatus:
    """
    Attributes:
        step_id (Union[Unset, str]):
        status (Union[Unset, str]):
        started_at (Union[None, Unset, datetime.datetime]):
        completed_at (Union[None, Unset, datetime.datetime]):
        job_id (Union[None, Unset, str]):
        error (Union[None, Unset, str]):
        output (Union['RunStepStatusOutputType0', None, Unset]):
        audit_hash (Union[None, Unset, str]): Audit-chain hash for the safety decision applied to this step,
            joined from the audit-chain entry produced when the step ran.
            Unset for skipped or upstream-failed steps where no decision
            was emitted. Dashboard surfaces this as a copy-on-click chip
            in the WorkflowNodeGovernanceOverlay.
             Example: 11473636023072616304.
    """

    step_id: Union[Unset, str] = UNSET
    status: Union[Unset, str] = UNSET
    started_at: Union[None, Unset, datetime.datetime] = UNSET
    completed_at: Union[None, Unset, datetime.datetime] = UNSET
    job_id: Union[None, Unset, str] = UNSET
    error: Union[None, Unset, str] = UNSET
    output: Union["RunStepStatusOutputType0", None, Unset] = UNSET
    audit_hash: Union[None, Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.run_step_status_output_type_0 import RunStepStatusOutputType0

        step_id = self.step_id

        status = self.status

        started_at: Union[None, Unset, str]
        if isinstance(self.started_at, Unset):
            started_at = UNSET
        elif isinstance(self.started_at, datetime.datetime):
            started_at = self.started_at.isoformat()
        else:
            started_at = self.started_at

        completed_at: Union[None, Unset, str]
        if isinstance(self.completed_at, Unset):
            completed_at = UNSET
        elif isinstance(self.completed_at, datetime.datetime):
            completed_at = self.completed_at.isoformat()
        else:
            completed_at = self.completed_at

        job_id: Union[None, Unset, str]
        if isinstance(self.job_id, Unset):
            job_id = UNSET
        else:
            job_id = self.job_id

        error: Union[None, Unset, str]
        if isinstance(self.error, Unset):
            error = UNSET
        else:
            error = self.error

        output: Union[Dict[str, Any], None, Unset]
        if isinstance(self.output, Unset):
            output = UNSET
        elif isinstance(self.output, RunStepStatusOutputType0):
            output = self.output.to_dict()
        else:
            output = self.output

        audit_hash: Union[None, Unset, str]
        if isinstance(self.audit_hash, Unset):
            audit_hash = UNSET
        else:
            audit_hash = self.audit_hash

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if step_id is not UNSET:
            field_dict["step_id"] = step_id
        if status is not UNSET:
            field_dict["status"] = status
        if started_at is not UNSET:
            field_dict["started_at"] = started_at
        if completed_at is not UNSET:
            field_dict["completed_at"] = completed_at
        if job_id is not UNSET:
            field_dict["job_id"] = job_id
        if error is not UNSET:
            field_dict["error"] = error
        if output is not UNSET:
            field_dict["output"] = output
        if audit_hash is not UNSET:
            field_dict["audit_hash"] = audit_hash

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.run_step_status_output_type_0 import RunStepStatusOutputType0

        d = src_dict.copy()
        step_id = d.pop("step_id", UNSET)

        status = d.pop("status", UNSET)

        def _parse_started_at(data: object) -> Union[None, Unset, datetime.datetime]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                started_at_type_0 = isoparse(data)

                return started_at_type_0
            except:  # noqa: E722
                pass
            return cast(Union[None, Unset, datetime.datetime], data)

        started_at = _parse_started_at(d.pop("started_at", UNSET))

        def _parse_completed_at(data: object) -> Union[None, Unset, datetime.datetime]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                completed_at_type_0 = isoparse(data)

                return completed_at_type_0
            except:  # noqa: E722
                pass
            return cast(Union[None, Unset, datetime.datetime], data)

        completed_at = _parse_completed_at(d.pop("completed_at", UNSET))

        def _parse_job_id(data: object) -> Union[None, Unset, str]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, str], data)

        job_id = _parse_job_id(d.pop("job_id", UNSET))

        def _parse_error(data: object) -> Union[None, Unset, str]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, str], data)

        error = _parse_error(d.pop("error", UNSET))

        def _parse_output(data: object) -> Union["RunStepStatusOutputType0", None, Unset]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                output_type_0 = RunStepStatusOutputType0.from_dict(data)

                return output_type_0
            except:  # noqa: E722
                pass
            return cast(Union["RunStepStatusOutputType0", None, Unset], data)

        output = _parse_output(d.pop("output", UNSET))

        def _parse_audit_hash(data: object) -> Union[None, Unset, str]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, str], data)

        audit_hash = _parse_audit_hash(d.pop("audit_hash", UNSET))

        run_step_status = cls(
            step_id=step_id,
            status=status,
            started_at=started_at,
            completed_at=completed_at,
            job_id=job_id,
            error=error,
            output=output,
            audit_hash=audit_hash,
        )

        run_step_status.additional_properties = d
        return run_step_status

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
