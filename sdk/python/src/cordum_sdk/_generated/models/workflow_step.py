from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.workflow_step_policy_gate import WorkflowStepPolicyGate
from ..types import UNSET, Unset
from typing import cast
from typing import cast, List
from typing import Dict
from typing import Union

if TYPE_CHECKING:
    from ..models.workflow_step_retry import WorkflowStepRetry
    from ..models.workflow_step_config import WorkflowStepConfig


T = TypeVar("T", bound="WorkflowStep")


@_attrs_define
class WorkflowStep:
    """
    Attributes:
        id (str):
        type (str): Step type (job, switch, parallel, loop, transform, storage, subworkflow, approval)
        name (Union[Unset, str]):
        depends_on (Union[Unset, List[str]]):
        config (Union[Unset, WorkflowStepConfig]):
        timeout_sec (Union[Unset, int]):
        retry (Union[Unset, WorkflowStepRetry]):
        policy_gate (Union[Unset, WorkflowStepPolicyGate]): Optional design-time policy hint. Populated at workflow-save
            time when the policy engine resolves a hint for this step.
            Unset means "no hint" — clients render no design-time icon
            and defer to runtime safety decision. NEVER defaults to
            "allow" when unset.
             Example: allow.
    """

    id: str
    type: str
    name: Union[Unset, str] = UNSET
    depends_on: Union[Unset, List[str]] = UNSET
    config: Union[Unset, "WorkflowStepConfig"] = UNSET
    timeout_sec: Union[Unset, int] = UNSET
    retry: Union[Unset, "WorkflowStepRetry"] = UNSET
    policy_gate: Union[Unset, WorkflowStepPolicyGate] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.workflow_step_retry import WorkflowStepRetry
        from ..models.workflow_step_config import WorkflowStepConfig

        id = self.id

        type = self.type

        name = self.name

        depends_on: Union[Unset, List[str]] = UNSET
        if not isinstance(self.depends_on, Unset):
            depends_on = self.depends_on

        config: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.config, Unset):
            config = self.config.to_dict()

        timeout_sec = self.timeout_sec

        retry: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.retry, Unset):
            retry = self.retry.to_dict()

        policy_gate: Union[Unset, str] = UNSET
        if not isinstance(self.policy_gate, Unset):
            policy_gate = self.policy_gate.value

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "type": type,
            }
        )
        if name is not UNSET:
            field_dict["name"] = name
        if depends_on is not UNSET:
            field_dict["depends_on"] = depends_on
        if config is not UNSET:
            field_dict["config"] = config
        if timeout_sec is not UNSET:
            field_dict["timeout_sec"] = timeout_sec
        if retry is not UNSET:
            field_dict["retry"] = retry
        if policy_gate is not UNSET:
            field_dict["policy_gate"] = policy_gate

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.workflow_step_retry import WorkflowStepRetry
        from ..models.workflow_step_config import WorkflowStepConfig

        d = src_dict.copy()
        id = d.pop("id")

        type = d.pop("type")

        name = d.pop("name", UNSET)

        depends_on = cast(List[str], d.pop("depends_on", UNSET))

        _config = d.pop("config", UNSET)
        config: Union[Unset, WorkflowStepConfig]
        if isinstance(_config, Unset):
            config = UNSET
        else:
            config = WorkflowStepConfig.from_dict(_config)

        timeout_sec = d.pop("timeout_sec", UNSET)

        _retry = d.pop("retry", UNSET)
        retry: Union[Unset, WorkflowStepRetry]
        if isinstance(_retry, Unset):
            retry = UNSET
        else:
            retry = WorkflowStepRetry.from_dict(_retry)

        _policy_gate = d.pop("policy_gate", UNSET)
        policy_gate: Union[Unset, WorkflowStepPolicyGate]
        if isinstance(_policy_gate, Unset):
            policy_gate = UNSET
        else:
            policy_gate = WorkflowStepPolicyGate(_policy_gate)

        workflow_step = cls(
            id=id,
            type=type,
            name=name,
            depends_on=depends_on,
            config=config,
            timeout_sec=timeout_sec,
            retry=retry,
            policy_gate=policy_gate,
        )

        workflow_step.additional_properties = d
        return workflow_step

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
