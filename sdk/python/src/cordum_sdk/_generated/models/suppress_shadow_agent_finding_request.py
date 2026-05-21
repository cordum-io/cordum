from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import Union
import datetime


T = TypeVar("T", bound="SuppressShadowAgentFindingRequest")


@_attrs_define
class SuppressShadowAgentFindingRequest:
    """
    Attributes:
        reason (Union[Unset, str]): Bounded reason for suppression; ≤512 bytes, secret markers stripped.
        suppressed_until (Union[Unset, datetime.datetime]): Optional time-bound hint; the store records but does not
            auto-revert.
    """

    reason: Union[Unset, str] = UNSET
    suppressed_until: Union[Unset, datetime.datetime] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        reason = self.reason

        suppressed_until: Union[Unset, str] = UNSET
        if not isinstance(self.suppressed_until, Unset):
            suppressed_until = self.suppressed_until.isoformat()

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if reason is not UNSET:
            field_dict["reason"] = reason
        if suppressed_until is not UNSET:
            field_dict["suppressed_until"] = suppressed_until

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        reason = d.pop("reason", UNSET)

        _suppressed_until = d.pop("suppressed_until", UNSET)
        suppressed_until: Union[Unset, datetime.datetime]
        if isinstance(_suppressed_until, Unset):
            suppressed_until = UNSET
        else:
            suppressed_until = isoparse(_suppressed_until)

        suppress_shadow_agent_finding_request = cls(
            reason=reason,
            suppressed_until=suppressed_until,
        )

        suppress_shadow_agent_finding_request.additional_properties = d
        return suppress_shadow_agent_finding_request

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
