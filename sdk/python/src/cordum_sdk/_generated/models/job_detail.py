from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import cast, List
from typing import cast, Union
from typing import Dict
from typing import Union
import datetime

if TYPE_CHECKING:
  from ..models.job_detail_result_type_0 import JobDetailResultType0
  from ..models.safety_decision import SafetyDecision
  from ..models.job_detail_labels import JobDetailLabels
  from ..models.delegation_lineage_view import DelegationLineageView





T = TypeVar("T", bound="JobDetail")


@_attrs_define
class JobDetail:
    """ 
        Attributes:
            id (Union[Unset, str]):
            state (Union[Unset, str]):
            topic (Union[Unset, str]):
            tenant (Union[Unset, str]):
            updated_at (Union[Unset, datetime.datetime]):
            trace_id (Union[Unset, str]):
            prompt (Union[Unset, str]):
            context_ptr (Union[None, Unset, str]):
            result_ptr (Union[None, Unset, str]):
            result (Union['JobDetailResultType0', None, Unset]):
            capability (Union[Unset, str]):
            risk_tags (Union[Unset, List[str]]):
            labels (Union[Unset, JobDetailLabels]):
            adapter_id (Union[Unset, str]):
            priority (Union[Unset, int]):
            created_at (Union[Unset, datetime.datetime]):
            started_at (Union[None, Unset, datetime.datetime]):
            completed_at (Union[None, Unset, datetime.datetime]):
            error (Union[None, Unset, str]):
            retry_count (Union[Unset, int]):
            decisions (Union[List['SafetyDecision'], None, Unset]):
            delegation (Union[Unset, DelegationLineageView]):
     """

    id: Union[Unset, str] = UNSET
    state: Union[Unset, str] = UNSET
    topic: Union[Unset, str] = UNSET
    tenant: Union[Unset, str] = UNSET
    updated_at: Union[Unset, datetime.datetime] = UNSET
    trace_id: Union[Unset, str] = UNSET
    prompt: Union[Unset, str] = UNSET
    context_ptr: Union[None, Unset, str] = UNSET
    result_ptr: Union[None, Unset, str] = UNSET
    result: Union['JobDetailResultType0', None, Unset] = UNSET
    capability: Union[Unset, str] = UNSET
    risk_tags: Union[Unset, List[str]] = UNSET
    labels: Union[Unset, 'JobDetailLabels'] = UNSET
    adapter_id: Union[Unset, str] = UNSET
    priority: Union[Unset, int] = UNSET
    created_at: Union[Unset, datetime.datetime] = UNSET
    started_at: Union[None, Unset, datetime.datetime] = UNSET
    completed_at: Union[None, Unset, datetime.datetime] = UNSET
    error: Union[None, Unset, str] = UNSET
    retry_count: Union[Unset, int] = UNSET
    decisions: Union[List['SafetyDecision'], None, Unset] = UNSET
    delegation: Union[Unset, 'DelegationLineageView'] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)


    def to_dict(self) -> Dict[str, Any]:
        from ..models.job_detail_result_type_0 import JobDetailResultType0
        from ..models.safety_decision import SafetyDecision
        from ..models.job_detail_labels import JobDetailLabels
        from ..models.delegation_lineage_view import DelegationLineageView
        id = self.id

        state = self.state

        topic = self.topic

        tenant = self.tenant

        updated_at: Union[Unset, str] = UNSET
        if not isinstance(self.updated_at, Unset):
            updated_at = self.updated_at.isoformat()

        trace_id = self.trace_id

        prompt = self.prompt

        context_ptr: Union[None, Unset, str]
        if isinstance(self.context_ptr, Unset):
            context_ptr = UNSET
        else:
            context_ptr = self.context_ptr

        result_ptr: Union[None, Unset, str]
        if isinstance(self.result_ptr, Unset):
            result_ptr = UNSET
        else:
            result_ptr = self.result_ptr

        result: Union[Dict[str, Any], None, Unset]
        if isinstance(self.result, Unset):
            result = UNSET
        elif isinstance(self.result, JobDetailResultType0):
            result = self.result.to_dict()
        else:
            result = self.result

        capability = self.capability

        risk_tags: Union[Unset, List[str]] = UNSET
        if not isinstance(self.risk_tags, Unset):
            risk_tags = self.risk_tags



        labels: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.labels, Unset):
            labels = self.labels.to_dict()

        adapter_id = self.adapter_id

        priority = self.priority

        created_at: Union[Unset, str] = UNSET
        if not isinstance(self.created_at, Unset):
            created_at = self.created_at.isoformat()

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

        error: Union[None, Unset, str]
        if isinstance(self.error, Unset):
            error = UNSET
        else:
            error = self.error

        retry_count = self.retry_count

        decisions: Union[List[Dict[str, Any]], None, Unset]
        if isinstance(self.decisions, Unset):
            decisions = UNSET
        elif isinstance(self.decisions, list):
            decisions = []
            for decisions_type_0_item_data in self.decisions:
                decisions_type_0_item = decisions_type_0_item_data.to_dict()
                decisions.append(decisions_type_0_item)


        else:
            decisions = self.decisions

        delegation: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.delegation, Unset):
            delegation = self.delegation.to_dict()


        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
        })
        if id is not UNSET:
            field_dict["id"] = id
        if state is not UNSET:
            field_dict["state"] = state
        if topic is not UNSET:
            field_dict["topic"] = topic
        if tenant is not UNSET:
            field_dict["tenant"] = tenant
        if updated_at is not UNSET:
            field_dict["updated_at"] = updated_at
        if trace_id is not UNSET:
            field_dict["trace_id"] = trace_id
        if prompt is not UNSET:
            field_dict["prompt"] = prompt
        if context_ptr is not UNSET:
            field_dict["context_ptr"] = context_ptr
        if result_ptr is not UNSET:
            field_dict["result_ptr"] = result_ptr
        if result is not UNSET:
            field_dict["result"] = result
        if capability is not UNSET:
            field_dict["capability"] = capability
        if risk_tags is not UNSET:
            field_dict["risk_tags"] = risk_tags
        if labels is not UNSET:
            field_dict["labels"] = labels
        if adapter_id is not UNSET:
            field_dict["adapter_id"] = adapter_id
        if priority is not UNSET:
            field_dict["priority"] = priority
        if created_at is not UNSET:
            field_dict["created_at"] = created_at
        if started_at is not UNSET:
            field_dict["started_at"] = started_at
        if completed_at is not UNSET:
            field_dict["completed_at"] = completed_at
        if error is not UNSET:
            field_dict["error"] = error
        if retry_count is not UNSET:
            field_dict["retry_count"] = retry_count
        if decisions is not UNSET:
            field_dict["decisions"] = decisions
        if delegation is not UNSET:
            field_dict["delegation"] = delegation

        return field_dict



    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.job_detail_result_type_0 import JobDetailResultType0
        from ..models.safety_decision import SafetyDecision
        from ..models.job_detail_labels import JobDetailLabels
        from ..models.delegation_lineage_view import DelegationLineageView
        d = src_dict.copy()
        id = d.pop("id", UNSET)

        state = d.pop("state", UNSET)

        topic = d.pop("topic", UNSET)

        tenant = d.pop("tenant", UNSET)

        _updated_at = d.pop("updated_at", UNSET)
        updated_at: Union[Unset, datetime.datetime]
        if isinstance(_updated_at,  Unset):
            updated_at = UNSET
        else:
            updated_at = isoparse(_updated_at)




        trace_id = d.pop("trace_id", UNSET)

        prompt = d.pop("prompt", UNSET)

        def _parse_context_ptr(data: object) -> Union[None, Unset, str]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, str], data)

        context_ptr = _parse_context_ptr(d.pop("context_ptr", UNSET))


        def _parse_result_ptr(data: object) -> Union[None, Unset, str]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, str], data)

        result_ptr = _parse_result_ptr(d.pop("result_ptr", UNSET))


        def _parse_result(data: object) -> Union['JobDetailResultType0', None, Unset]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                result_type_0 = JobDetailResultType0.from_dict(data)



                return result_type_0
            except: # noqa: E722
                pass
            return cast(Union['JobDetailResultType0', None, Unset], data)

        result = _parse_result(d.pop("result", UNSET))


        capability = d.pop("capability", UNSET)

        risk_tags = cast(List[str], d.pop("risk_tags", UNSET))


        _labels = d.pop("labels", UNSET)
        labels: Union[Unset, JobDetailLabels]
        if isinstance(_labels,  Unset):
            labels = UNSET
        else:
            labels = JobDetailLabels.from_dict(_labels)




        adapter_id = d.pop("adapter_id", UNSET)

        priority = d.pop("priority", UNSET)

        _created_at = d.pop("created_at", UNSET)
        created_at: Union[Unset, datetime.datetime]
        if isinstance(_created_at,  Unset):
            created_at = UNSET
        else:
            created_at = isoparse(_created_at)




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
            except: # noqa: E722
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
            except: # noqa: E722
                pass
            return cast(Union[None, Unset, datetime.datetime], data)

        completed_at = _parse_completed_at(d.pop("completed_at", UNSET))


        def _parse_error(data: object) -> Union[None, Unset, str]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, str], data)

        error = _parse_error(d.pop("error", UNSET))


        retry_count = d.pop("retry_count", UNSET)

        def _parse_decisions(data: object) -> Union[List['SafetyDecision'], None, Unset]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, list):
                    raise TypeError()
                decisions_type_0 = []
                _decisions_type_0 = data
                for decisions_type_0_item_data in (_decisions_type_0):
                    decisions_type_0_item = SafetyDecision.from_dict(decisions_type_0_item_data)



                    decisions_type_0.append(decisions_type_0_item)

                return decisions_type_0
            except: # noqa: E722
                pass
            return cast(Union[List['SafetyDecision'], None, Unset], data)

        decisions = _parse_decisions(d.pop("decisions", UNSET))


        _delegation = d.pop("delegation", UNSET)
        delegation: Union[Unset, DelegationLineageView]
        if isinstance(_delegation,  Unset):
            delegation = UNSET
        else:
            delegation = DelegationLineageView.from_dict(_delegation)




        job_detail = cls(
            id=id,
            state=state,
            topic=topic,
            tenant=tenant,
            updated_at=updated_at,
            trace_id=trace_id,
            prompt=prompt,
            context_ptr=context_ptr,
            result_ptr=result_ptr,
            result=result,
            capability=capability,
            risk_tags=risk_tags,
            labels=labels,
            adapter_id=adapter_id,
            priority=priority,
            created_at=created_at,
            started_at=started_at,
            completed_at=completed_at,
            error=error,
            retry_count=retry_count,
            decisions=decisions,
            delegation=delegation,
        )


        job_detail.additional_properties = d
        return job_detail

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
