from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.edge_runtime_file_summary_operation import EdgeRuntimeFileSummaryOperation
from ..types import UNSET, Unset
from typing import Union


T = TypeVar("T", bound="EdgeRuntimeFileSummary")


@_attrs_define
class EdgeRuntimeFileSummary:
    """
    Attributes:
        operation (Union[Unset, EdgeRuntimeFileSummaryOperation]):
        path_redacted (Union[Unset, str]):
    """

    operation: Union[Unset, EdgeRuntimeFileSummaryOperation] = UNSET
    path_redacted: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        operation: Union[Unset, str] = UNSET
        if not isinstance(self.operation, Unset):
            operation = self.operation.value

        path_redacted = self.path_redacted

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if operation is not UNSET:
            field_dict["operation"] = operation
        if path_redacted is not UNSET:
            field_dict["path_redacted"] = path_redacted

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        _operation = d.pop("operation", UNSET)
        operation: Union[Unset, EdgeRuntimeFileSummaryOperation]
        if isinstance(_operation, Unset):
            operation = UNSET
        else:
            operation = EdgeRuntimeFileSummaryOperation(_operation)

        path_redacted = d.pop("path_redacted", UNSET)

        edge_runtime_file_summary = cls(
            operation=operation,
            path_redacted=path_redacted,
        )

        edge_runtime_file_summary.additional_properties = d
        return edge_runtime_file_summary

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
