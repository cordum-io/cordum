from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset


T = TypeVar("T", bound="IngestBinaryVerifyError")


@_attrs_define
class IngestBinaryVerifyError:
    """One per-event rejection from a partial-success `ingestBinaryVerify`
    request. The accepted events have already been persisted.

        Attributes:
            index (int): Zero-based index of the rejected event within the request
                `events` array.
            error (str): Human-readable validation reason, sourced from
                `model.BinaryVerifyEvent.Validate`.
    """

    index: int
    error: str
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        index = self.index

        error = self.error

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "index": index,
                "error": error,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        index = d.pop("index")

        error = d.pop("error")

        ingest_binary_verify_error = cls(
            index=index,
            error=error,
        )

        ingest_binary_verify_error.additional_properties = d
        return ingest_binary_verify_error

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
