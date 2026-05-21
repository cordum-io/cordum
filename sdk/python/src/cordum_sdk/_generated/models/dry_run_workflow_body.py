from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast
from typing import Dict
from typing import Union

if TYPE_CHECKING:
    from ..models.dry_run_workflow_body_environment import DryRunWorkflowBodyEnvironment
    from ..models.dry_run_workflow_body_input import DryRunWorkflowBodyInput


T = TypeVar("T", bound="DryRunWorkflowBody")


@_attrs_define
class DryRunWorkflowBody:
    """
    Attributes:
        input_ (Union[Unset, DryRunWorkflowBodyInput]):
        environment (Union[Unset, DryRunWorkflowBodyEnvironment]):
    """

    input_: Union[Unset, "DryRunWorkflowBodyInput"] = UNSET
    environment: Union[Unset, "DryRunWorkflowBodyEnvironment"] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.dry_run_workflow_body_environment import DryRunWorkflowBodyEnvironment
        from ..models.dry_run_workflow_body_input import DryRunWorkflowBodyInput

        input_: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.input_, Unset):
            input_ = self.input_.to_dict()

        environment: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.environment, Unset):
            environment = self.environment.to_dict()

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if input_ is not UNSET:
            field_dict["input"] = input_
        if environment is not UNSET:
            field_dict["environment"] = environment

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.dry_run_workflow_body_environment import DryRunWorkflowBodyEnvironment
        from ..models.dry_run_workflow_body_input import DryRunWorkflowBodyInput

        d = src_dict.copy()
        _input_ = d.pop("input", UNSET)
        input_: Union[Unset, DryRunWorkflowBodyInput]
        if isinstance(_input_, Unset):
            input_ = UNSET
        else:
            input_ = DryRunWorkflowBodyInput.from_dict(_input_)

        _environment = d.pop("environment", UNSET)
        environment: Union[Unset, DryRunWorkflowBodyEnvironment]
        if isinstance(_environment, Unset):
            environment = UNSET
        else:
            environment = DryRunWorkflowBodyEnvironment.from_dict(_environment)

        dry_run_workflow_body = cls(
            input_=input_,
            environment=environment,
        )

        dry_run_workflow_body.additional_properties = d
        return dry_run_workflow_body

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
