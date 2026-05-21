from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import Union


T = TypeVar("T", bound="WorkflowStepRetry")


@_attrs_define
class WorkflowStepRetry:
    """
    Attributes:
        max_attempts (Union[Unset, int]):
        backoff_sec (Union[Unset, int]):
    """

    max_attempts: Union[Unset, int] = UNSET
    backoff_sec: Union[Unset, int] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        max_attempts = self.max_attempts

        backoff_sec = self.backoff_sec

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if max_attempts is not UNSET:
            field_dict["max_attempts"] = max_attempts
        if backoff_sec is not UNSET:
            field_dict["backoff_sec"] = backoff_sec

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        max_attempts = d.pop("max_attempts", UNSET)

        backoff_sec = d.pop("backoff_sec", UNSET)

        workflow_step_retry = cls(
            max_attempts=max_attempts,
            backoff_sec=backoff_sec,
        )

        workflow_step_retry.additional_properties = d
        return workflow_step_retry

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
