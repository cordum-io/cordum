from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast
from typing import cast, List
from typing import Dict
from typing import Union

if TYPE_CHECKING:
    from ..models.run_step_status import RunStepStatus


T = TypeVar("T", bound="DryRunResult")


@_attrs_define
class DryRunResult:
    """
    Attributes:
        steps (Union[Unset, List['RunStepStatus']]):
        warnings (Union[Unset, List[str]]):
        estimated_duration_sec (Union[Unset, float]):
    """

    steps: Union[Unset, List["RunStepStatus"]] = UNSET
    warnings: Union[Unset, List[str]] = UNSET
    estimated_duration_sec: Union[Unset, float] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.run_step_status import RunStepStatus

        steps: Union[Unset, List[Dict[str, Any]]] = UNSET
        if not isinstance(self.steps, Unset):
            steps = []
            for steps_item_data in self.steps:
                steps_item = steps_item_data.to_dict()
                steps.append(steps_item)

        warnings: Union[Unset, List[str]] = UNSET
        if not isinstance(self.warnings, Unset):
            warnings = self.warnings

        estimated_duration_sec = self.estimated_duration_sec

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if steps is not UNSET:
            field_dict["steps"] = steps
        if warnings is not UNSET:
            field_dict["warnings"] = warnings
        if estimated_duration_sec is not UNSET:
            field_dict["estimated_duration_sec"] = estimated_duration_sec

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.run_step_status import RunStepStatus

        d = src_dict.copy()
        steps = []
        _steps = d.pop("steps", UNSET)
        for steps_item_data in _steps or []:
            steps_item = RunStepStatus.from_dict(steps_item_data)

            steps.append(steps_item)

        warnings = cast(List[str], d.pop("warnings", UNSET))

        estimated_duration_sec = d.pop("estimated_duration_sec", UNSET)

        dry_run_result = cls(
            steps=steps,
            warnings=warnings,
            estimated_duration_sec=estimated_duration_sec,
        )

        dry_run_result.additional_properties = d
        return dry_run_result

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
