from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.run_summary_status import RunSummaryStatus
from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import cast, Union
from typing import Dict
from typing import Union
import datetime

if TYPE_CHECKING:
  from ..models.run_detail_input import RunDetailInput
  from ..models.run_detail_output_type_0 import RunDetailOutputType0
  from ..models.run_summary_error_type_0 import RunSummaryErrorType0
  from ..models.run_detail_steps import RunDetailSteps





T = TypeVar("T", bound="RunDetail")


@_attrs_define
class RunDetail:
    """ 
        Attributes:
            id (Union[Unset, str]):
            workflow_id (Union[Unset, str]):
            status (Union[Unset, RunSummaryStatus]): Workflow run lifecycle status (lowercase)
            started_at (Union[Unset, datetime.datetime]):
            completed_at (Union[None, Unset, datetime.datetime]):
            error (Union['RunSummaryErrorType0', None, Unset]): Error details as key-value map (e.g. {code, message})
            input_ (Union[Unset, RunDetailInput]):
            output (Union['RunDetailOutputType0', None, Unset]):
            steps (Union[Unset, RunDetailSteps]): Map of step ID to step run status
            created_by (Union[Unset, str]):
            dry_run (Union[Unset, bool]):
     """

    id: Union[Unset, str] = UNSET
    workflow_id: Union[Unset, str] = UNSET
    status: Union[Unset, RunSummaryStatus] = UNSET
    started_at: Union[Unset, datetime.datetime] = UNSET
    completed_at: Union[None, Unset, datetime.datetime] = UNSET
    error: Union['RunSummaryErrorType0', None, Unset] = UNSET
    input_: Union[Unset, 'RunDetailInput'] = UNSET
    output: Union['RunDetailOutputType0', None, Unset] = UNSET
    steps: Union[Unset, 'RunDetailSteps'] = UNSET
    created_by: Union[Unset, str] = UNSET
    dry_run: Union[Unset, bool] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)


    def to_dict(self) -> Dict[str, Any]:
        from ..models.run_detail_input import RunDetailInput
        from ..models.run_detail_output_type_0 import RunDetailOutputType0
        from ..models.run_summary_error_type_0 import RunSummaryErrorType0
        from ..models.run_detail_steps import RunDetailSteps
        id = self.id

        workflow_id = self.workflow_id

        status: Union[Unset, str] = UNSET
        if not isinstance(self.status, Unset):
            status = self.status.value


        started_at: Union[Unset, str] = UNSET
        if not isinstance(self.started_at, Unset):
            started_at = self.started_at.isoformat()

        completed_at: Union[None, Unset, str]
        if isinstance(self.completed_at, Unset):
            completed_at = UNSET
        elif isinstance(self.completed_at, datetime.datetime):
            completed_at = self.completed_at.isoformat()
        else:
            completed_at = self.completed_at

        error: Union[Dict[str, Any], None, Unset]
        if isinstance(self.error, Unset):
            error = UNSET
        elif isinstance(self.error, RunSummaryErrorType0):
            error = self.error.to_dict()
        else:
            error = self.error

        input_: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.input_, Unset):
            input_ = self.input_.to_dict()

        output: Union[Dict[str, Any], None, Unset]
        if isinstance(self.output, Unset):
            output = UNSET
        elif isinstance(self.output, RunDetailOutputType0):
            output = self.output.to_dict()
        else:
            output = self.output

        steps: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.steps, Unset):
            steps = self.steps.to_dict()

        created_by = self.created_by

        dry_run = self.dry_run


        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
        })
        if id is not UNSET:
            field_dict["id"] = id
        if workflow_id is not UNSET:
            field_dict["workflow_id"] = workflow_id
        if status is not UNSET:
            field_dict["status"] = status
        if started_at is not UNSET:
            field_dict["started_at"] = started_at
        if completed_at is not UNSET:
            field_dict["completed_at"] = completed_at
        if error is not UNSET:
            field_dict["error"] = error
        if input_ is not UNSET:
            field_dict["input"] = input_
        if output is not UNSET:
            field_dict["output"] = output
        if steps is not UNSET:
            field_dict["steps"] = steps
        if created_by is not UNSET:
            field_dict["created_by"] = created_by
        if dry_run is not UNSET:
            field_dict["dry_run"] = dry_run

        return field_dict



    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.run_detail_input import RunDetailInput
        from ..models.run_detail_output_type_0 import RunDetailOutputType0
        from ..models.run_summary_error_type_0 import RunSummaryErrorType0
        from ..models.run_detail_steps import RunDetailSteps
        d = src_dict.copy()
        id = d.pop("id", UNSET)

        workflow_id = d.pop("workflow_id", UNSET)

        _status = d.pop("status", UNSET)
        status: Union[Unset, RunSummaryStatus]
        if isinstance(_status,  Unset):
            status = UNSET
        else:
            status = RunSummaryStatus(_status)




        _started_at = d.pop("started_at", UNSET)
        started_at: Union[Unset, datetime.datetime]
        if isinstance(_started_at,  Unset):
            started_at = UNSET
        else:
            started_at = isoparse(_started_at)




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
            except: # noqa: E722
                pass
            return cast(Union[None, Unset, datetime.datetime], data)

        completed_at = _parse_completed_at(d.pop("completed_at", UNSET))


        def _parse_error(data: object) -> Union['RunSummaryErrorType0', None, Unset]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                error_type_0 = RunSummaryErrorType0.from_dict(data)



                return error_type_0
            except: # noqa: E722
                pass
            return cast(Union['RunSummaryErrorType0', None, Unset], data)

        error = _parse_error(d.pop("error", UNSET))


        _input_ = d.pop("input", UNSET)
        input_: Union[Unset, RunDetailInput]
        if isinstance(_input_,  Unset):
            input_ = UNSET
        else:
            input_ = RunDetailInput.from_dict(_input_)




        def _parse_output(data: object) -> Union['RunDetailOutputType0', None, Unset]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                output_type_0 = RunDetailOutputType0.from_dict(data)



                return output_type_0
            except: # noqa: E722
                pass
            return cast(Union['RunDetailOutputType0', None, Unset], data)

        output = _parse_output(d.pop("output", UNSET))


        _steps = d.pop("steps", UNSET)
        steps: Union[Unset, RunDetailSteps]
        if isinstance(_steps,  Unset):
            steps = UNSET
        else:
            steps = RunDetailSteps.from_dict(_steps)




        created_by = d.pop("created_by", UNSET)

        dry_run = d.pop("dry_run", UNSET)

        run_detail = cls(
            id=id,
            workflow_id=workflow_id,
            status=status,
            started_at=started_at,
            completed_at=completed_at,
            error=error,
            input_=input_,
            output=output,
            steps=steps,
            created_by=created_by,
            dry_run=dry_run,
        )


        run_detail.additional_properties = d
        return run_detail

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
