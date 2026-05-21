from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import Union


T = TypeVar("T", bound="EdgeRuntimeProcessSummary")


@_attrs_define
class EdgeRuntimeProcessSummary:
    """Bounded redacted summary of a process exec event. Raw argv / env / cmdline are forbidden.

    Attributes:
        executable_basename (Union[Unset, str]):
        executable_sha256 (Union[Unset, str]):
        argument_count (Union[Unset, int]):
    """

    executable_basename: Union[Unset, str] = UNSET
    executable_sha256: Union[Unset, str] = UNSET
    argument_count: Union[Unset, int] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        executable_basename = self.executable_basename

        executable_sha256 = self.executable_sha256

        argument_count = self.argument_count

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if executable_basename is not UNSET:
            field_dict["executable_basename"] = executable_basename
        if executable_sha256 is not UNSET:
            field_dict["executable_sha256"] = executable_sha256
        if argument_count is not UNSET:
            field_dict["argument_count"] = argument_count

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        executable_basename = d.pop("executable_basename", UNSET)

        executable_sha256 = d.pop("executable_sha256", UNSET)

        argument_count = d.pop("argument_count", UNSET)

        edge_runtime_process_summary = cls(
            executable_basename=executable_basename,
            executable_sha256=executable_sha256,
            argument_count=argument_count,
        )

        edge_runtime_process_summary.additional_properties = d
        return edge_runtime_process_summary

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
