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
    from ..models.ingest_binary_verify_error import IngestBinaryVerifyError


T = TypeVar("T", bound="IngestBinaryVerifyResponse")


@_attrs_define
class IngestBinaryVerifyResponse:
    """Result of a binary-verify batch ingest. `accepted + rejected` may
    be less than the submitted batch only when a hard error prevented
    decoding; per-event validation failures are surfaced in `errors`.

        Attributes:
            accepted (int): Number of events persisted to the audit chain.
            rejected (int): Number of events rejected by per-event validation.
            errors (Union[Unset, List['IngestBinaryVerifyError']]): Per-event rejections. Omitted when rejected is 0.
    """

    accepted: int
    rejected: int
    errors: Union[Unset, List["IngestBinaryVerifyError"]] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.ingest_binary_verify_error import IngestBinaryVerifyError

        accepted = self.accepted

        rejected = self.rejected

        errors: Union[Unset, List[Dict[str, Any]]] = UNSET
        if not isinstance(self.errors, Unset):
            errors = []
            for errors_item_data in self.errors:
                errors_item = errors_item_data.to_dict()
                errors.append(errors_item)

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "accepted": accepted,
                "rejected": rejected,
            }
        )
        if errors is not UNSET:
            field_dict["errors"] = errors

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.ingest_binary_verify_error import IngestBinaryVerifyError

        d = src_dict.copy()
        accepted = d.pop("accepted")

        rejected = d.pop("rejected")

        errors = []
        _errors = d.pop("errors", UNSET)
        for errors_item_data in _errors or []:
            errors_item = IngestBinaryVerifyError.from_dict(errors_item_data)

            errors.append(errors_item)

        ingest_binary_verify_response = cls(
            accepted=accepted,
            rejected=rejected,
            errors=errors,
        )

        ingest_binary_verify_response.additional_properties = d
        return ingest_binary_verify_response

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
