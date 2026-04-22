from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.submit_job_request_priority import SubmitJobRequestPriority
from ..types import UNSET, Unset
from typing import cast
from typing import cast, List
from typing import Dict
from typing import Union

if TYPE_CHECKING:
  from ..models.submit_job_request_context import SubmitJobRequestContext
  from ..models.submit_job_request_labels import SubmitJobRequestLabels





T = TypeVar("T", bound="SubmitJobRequest")


@_attrs_define
class SubmitJobRequest:
    """ 
        Attributes:
            prompt (str): The job payload / prompt text
            topic (str): Routing topic for worker dispatch Default: 'job.default'.
            adapter_id (Union[Unset, str]): Adapter identifier for the target worker
            priority (Union[Unset, SubmitJobRequestPriority]): Job scheduling priority Default:
                SubmitJobRequestPriority.INTERACTIVE.
            context (Union[Unset, SubmitJobRequestContext]): Arbitrary context payload for the job
            memory_id (Union[Unset, str]): Memory store identifier for context retrieval
            context_mode (Union[Unset, str]): Context retrieval mode (e.g. full, summary)
            tenant_id (Union[Unset, str]): Tenant identifier for multi-tenant isolation
            principal_id (Union[Unset, str]): Principal (user/service) submitting the job
            actor_id (Union[Unset, str]): Actor identifier for audit attribution
            actor_type (Union[Unset, str]): Actor type (human, agent, system)
            idempotency_key (Union[Unset, str]): Client-provided idempotency key to prevent duplicate jobs
            pack_id (Union[Unset, str]): Integration pack identifier
            capability (Union[Unset, str]): Required worker capability for dispatch
            risk_tags (Union[Unset, List[str]]): Risk classification tags for policy evaluation
            requires (Union[Unset, List[str]]): Required worker capabilities for dispatch matching
            org_id (Union[Unset, str]): Organization identifier for hierarchical tenancy
            team_id (Union[Unset, str]): Team identifier within the organization
            project_id (Union[Unset, str]): Project identifier for scoped job tracking
            labels (Union[Unset, SubmitJobRequestLabels]): Arbitrary key-value labels for filtering and blast radius
            max_input_tokens (Union[Unset, int]): Maximum input token budget for the job
            allow_summarization (Union[Unset, bool]): Whether the worker may summarize oversized input
            allow_retrieval (Union[Unset, bool]): Whether the worker may retrieve external context
            tags (Union[Unset, List[str]]): Searchable tags for job discovery
            max_output_tokens (Union[Unset, int]): Maximum output token budget
            max_total_tokens (Union[Unset, int]): Maximum total (input + output) token budget
            deadline_ms (Union[Unset, int]): Job deadline in milliseconds from submission (0 = no deadline)
     """

    prompt: str
    topic: str = 'job.default'
    adapter_id: Union[Unset, str] = UNSET
    priority: Union[Unset, SubmitJobRequestPriority] = SubmitJobRequestPriority.INTERACTIVE
    context: Union[Unset, 'SubmitJobRequestContext'] = UNSET
    memory_id: Union[Unset, str] = UNSET
    context_mode: Union[Unset, str] = UNSET
    tenant_id: Union[Unset, str] = UNSET
    principal_id: Union[Unset, str] = UNSET
    actor_id: Union[Unset, str] = UNSET
    actor_type: Union[Unset, str] = UNSET
    idempotency_key: Union[Unset, str] = UNSET
    pack_id: Union[Unset, str] = UNSET
    capability: Union[Unset, str] = UNSET
    risk_tags: Union[Unset, List[str]] = UNSET
    requires: Union[Unset, List[str]] = UNSET
    org_id: Union[Unset, str] = UNSET
    team_id: Union[Unset, str] = UNSET
    project_id: Union[Unset, str] = UNSET
    labels: Union[Unset, 'SubmitJobRequestLabels'] = UNSET
    max_input_tokens: Union[Unset, int] = UNSET
    allow_summarization: Union[Unset, bool] = UNSET
    allow_retrieval: Union[Unset, bool] = UNSET
    tags: Union[Unset, List[str]] = UNSET
    max_output_tokens: Union[Unset, int] = UNSET
    max_total_tokens: Union[Unset, int] = UNSET
    deadline_ms: Union[Unset, int] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)


    def to_dict(self) -> Dict[str, Any]:
        from ..models.submit_job_request_context import SubmitJobRequestContext
        from ..models.submit_job_request_labels import SubmitJobRequestLabels
        prompt = self.prompt

        topic = self.topic

        adapter_id = self.adapter_id

        priority: Union[Unset, str] = UNSET
        if not isinstance(self.priority, Unset):
            priority = self.priority.value


        context: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.context, Unset):
            context = self.context.to_dict()

        memory_id = self.memory_id

        context_mode = self.context_mode

        tenant_id = self.tenant_id

        principal_id = self.principal_id

        actor_id = self.actor_id

        actor_type = self.actor_type

        idempotency_key = self.idempotency_key

        pack_id = self.pack_id

        capability = self.capability

        risk_tags: Union[Unset, List[str]] = UNSET
        if not isinstance(self.risk_tags, Unset):
            risk_tags = self.risk_tags



        requires: Union[Unset, List[str]] = UNSET
        if not isinstance(self.requires, Unset):
            requires = self.requires



        org_id = self.org_id

        team_id = self.team_id

        project_id = self.project_id

        labels: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.labels, Unset):
            labels = self.labels.to_dict()

        max_input_tokens = self.max_input_tokens

        allow_summarization = self.allow_summarization

        allow_retrieval = self.allow_retrieval

        tags: Union[Unset, List[str]] = UNSET
        if not isinstance(self.tags, Unset):
            tags = self.tags



        max_output_tokens = self.max_output_tokens

        max_total_tokens = self.max_total_tokens

        deadline_ms = self.deadline_ms


        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
            "prompt": prompt,
            "topic": topic,
        })
        if adapter_id is not UNSET:
            field_dict["adapter_id"] = adapter_id
        if priority is not UNSET:
            field_dict["priority"] = priority
        if context is not UNSET:
            field_dict["context"] = context
        if memory_id is not UNSET:
            field_dict["memory_id"] = memory_id
        if context_mode is not UNSET:
            field_dict["context_mode"] = context_mode
        if tenant_id is not UNSET:
            field_dict["tenant_id"] = tenant_id
        if principal_id is not UNSET:
            field_dict["principal_id"] = principal_id
        if actor_id is not UNSET:
            field_dict["actor_id"] = actor_id
        if actor_type is not UNSET:
            field_dict["actor_type"] = actor_type
        if idempotency_key is not UNSET:
            field_dict["idempotency_key"] = idempotency_key
        if pack_id is not UNSET:
            field_dict["pack_id"] = pack_id
        if capability is not UNSET:
            field_dict["capability"] = capability
        if risk_tags is not UNSET:
            field_dict["risk_tags"] = risk_tags
        if requires is not UNSET:
            field_dict["requires"] = requires
        if org_id is not UNSET:
            field_dict["org_id"] = org_id
        if team_id is not UNSET:
            field_dict["team_id"] = team_id
        if project_id is not UNSET:
            field_dict["project_id"] = project_id
        if labels is not UNSET:
            field_dict["labels"] = labels
        if max_input_tokens is not UNSET:
            field_dict["max_input_tokens"] = max_input_tokens
        if allow_summarization is not UNSET:
            field_dict["allow_summarization"] = allow_summarization
        if allow_retrieval is not UNSET:
            field_dict["allow_retrieval"] = allow_retrieval
        if tags is not UNSET:
            field_dict["tags"] = tags
        if max_output_tokens is not UNSET:
            field_dict["max_output_tokens"] = max_output_tokens
        if max_total_tokens is not UNSET:
            field_dict["max_total_tokens"] = max_total_tokens
        if deadline_ms is not UNSET:
            field_dict["deadline_ms"] = deadline_ms

        return field_dict



    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.submit_job_request_context import SubmitJobRequestContext
        from ..models.submit_job_request_labels import SubmitJobRequestLabels
        d = src_dict.copy()
        prompt = d.pop("prompt")

        topic = d.pop("topic")

        adapter_id = d.pop("adapter_id", UNSET)

        _priority = d.pop("priority", UNSET)
        priority: Union[Unset, SubmitJobRequestPriority]
        if isinstance(_priority,  Unset):
            priority = UNSET
        else:
            priority = SubmitJobRequestPriority(_priority)




        _context = d.pop("context", UNSET)
        context: Union[Unset, SubmitJobRequestContext]
        if isinstance(_context,  Unset):
            context = UNSET
        else:
            context = SubmitJobRequestContext.from_dict(_context)




        memory_id = d.pop("memory_id", UNSET)

        context_mode = d.pop("context_mode", UNSET)

        tenant_id = d.pop("tenant_id", UNSET)

        principal_id = d.pop("principal_id", UNSET)

        actor_id = d.pop("actor_id", UNSET)

        actor_type = d.pop("actor_type", UNSET)

        idempotency_key = d.pop("idempotency_key", UNSET)

        pack_id = d.pop("pack_id", UNSET)

        capability = d.pop("capability", UNSET)

        risk_tags = cast(List[str], d.pop("risk_tags", UNSET))


        requires = cast(List[str], d.pop("requires", UNSET))


        org_id = d.pop("org_id", UNSET)

        team_id = d.pop("team_id", UNSET)

        project_id = d.pop("project_id", UNSET)

        _labels = d.pop("labels", UNSET)
        labels: Union[Unset, SubmitJobRequestLabels]
        if isinstance(_labels,  Unset):
            labels = UNSET
        else:
            labels = SubmitJobRequestLabels.from_dict(_labels)




        max_input_tokens = d.pop("max_input_tokens", UNSET)

        allow_summarization = d.pop("allow_summarization", UNSET)

        allow_retrieval = d.pop("allow_retrieval", UNSET)

        tags = cast(List[str], d.pop("tags", UNSET))


        max_output_tokens = d.pop("max_output_tokens", UNSET)

        max_total_tokens = d.pop("max_total_tokens", UNSET)

        deadline_ms = d.pop("deadline_ms", UNSET)

        submit_job_request = cls(
            prompt=prompt,
            topic=topic,
            adapter_id=adapter_id,
            priority=priority,
            context=context,
            memory_id=memory_id,
            context_mode=context_mode,
            tenant_id=tenant_id,
            principal_id=principal_id,
            actor_id=actor_id,
            actor_type=actor_type,
            idempotency_key=idempotency_key,
            pack_id=pack_id,
            capability=capability,
            risk_tags=risk_tags,
            requires=requires,
            org_id=org_id,
            team_id=team_id,
            project_id=project_id,
            labels=labels,
            max_input_tokens=max_input_tokens,
            allow_summarization=allow_summarization,
            allow_retrieval=allow_retrieval,
            tags=tags,
            max_output_tokens=max_output_tokens,
            max_total_tokens=max_total_tokens,
            deadline_ms=deadline_ms,
        )


        submit_job_request.additional_properties = d
        return submit_job_request

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
