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
    from ..models.workflow_definition_config import WorkflowDefinitionConfig
    from ..models.workflow_definition_steps import WorkflowDefinitionSteps


T = TypeVar("T", bound="WorkflowDefinition")


@_attrs_define
class WorkflowDefinition:
    """
    Attributes:
        id (str):
        name (str):
        steps (WorkflowDefinitionSteps): Map of step ID to step definition
        org_id (Union[Unset, str]):
        team_id (Union[Unset, str]):
        description (Union[Unset, str]):
        version (Union[Unset, str]):
        timeout_sec (Union[Unset, int]):
        config (Union[Unset, WorkflowDefinitionConfig]):
    """

    id: str
    name: str
    steps: "WorkflowDefinitionSteps"
    org_id: Union[Unset, str] = UNSET
    team_id: Union[Unset, str] = UNSET
    description: Union[Unset, str] = UNSET
    version: Union[Unset, str] = UNSET
    timeout_sec: Union[Unset, int] = UNSET
    config: Union[Unset, "WorkflowDefinitionConfig"] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.workflow_definition_config import WorkflowDefinitionConfig
        from ..models.workflow_definition_steps import WorkflowDefinitionSteps

        id = self.id

        name = self.name

        steps = self.steps.to_dict()

        org_id = self.org_id

        team_id = self.team_id

        description = self.description

        version = self.version

        timeout_sec = self.timeout_sec

        config: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.config, Unset):
            config = self.config.to_dict()

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "name": name,
                "steps": steps,
            }
        )
        if org_id is not UNSET:
            field_dict["org_id"] = org_id
        if team_id is not UNSET:
            field_dict["team_id"] = team_id
        if description is not UNSET:
            field_dict["description"] = description
        if version is not UNSET:
            field_dict["version"] = version
        if timeout_sec is not UNSET:
            field_dict["timeout_sec"] = timeout_sec
        if config is not UNSET:
            field_dict["config"] = config

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.workflow_definition_config import WorkflowDefinitionConfig
        from ..models.workflow_definition_steps import WorkflowDefinitionSteps

        d = src_dict.copy()
        id = d.pop("id")

        name = d.pop("name")

        steps = WorkflowDefinitionSteps.from_dict(d.pop("steps"))

        org_id = d.pop("org_id", UNSET)

        team_id = d.pop("team_id", UNSET)

        description = d.pop("description", UNSET)

        version = d.pop("version", UNSET)

        timeout_sec = d.pop("timeout_sec", UNSET)

        _config = d.pop("config", UNSET)
        config: Union[Unset, WorkflowDefinitionConfig]
        if isinstance(_config, Unset):
            config = UNSET
        else:
            config = WorkflowDefinitionConfig.from_dict(_config)

        workflow_definition = cls(
            id=id,
            name=name,
            steps=steps,
            org_id=org_id,
            team_id=team_id,
            description=description,
            version=version,
            timeout_sec=timeout_sec,
            config=config,
        )

        workflow_definition.additional_properties = d
        return workflow_definition

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
