from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.create_shadow_exception_request_scope_risk_level import (
    CreateShadowExceptionRequestScopeRiskLevel,
)
from ..models.create_shadow_exception_request_scope_source_type import (
    CreateShadowExceptionRequestScopeSourceType,
)
from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import cast, List
from typing import Union
import datetime


T = TypeVar("T", bound="CreateShadowExceptionRequest")


@_attrs_define
class CreateShadowExceptionRequest:
    """
    Attributes:
        expires_at (datetime.datetime): MUST be in the future and within 90 days of now.
        scope_source_type (CreateShadowExceptionRequestScopeSourceType):
        scope_risk_level (CreateShadowExceptionRequestScopeRiskLevel):
        reason (Union[Unset, str]):
        scope_source_id (Union[Unset, str]):
        scope_signal_set (Union[Unset, List[str]]):
    """

    expires_at: datetime.datetime
    scope_source_type: CreateShadowExceptionRequestScopeSourceType
    scope_risk_level: CreateShadowExceptionRequestScopeRiskLevel
    reason: Union[Unset, str] = UNSET
    scope_source_id: Union[Unset, str] = UNSET
    scope_signal_set: Union[Unset, List[str]] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        expires_at = self.expires_at.isoformat()

        scope_source_type = self.scope_source_type.value

        scope_risk_level = self.scope_risk_level.value

        reason = self.reason

        scope_source_id = self.scope_source_id

        scope_signal_set: Union[Unset, List[str]] = UNSET
        if not isinstance(self.scope_signal_set, Unset):
            scope_signal_set = self.scope_signal_set

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "expires_at": expires_at,
                "scope_source_type": scope_source_type,
                "scope_risk_level": scope_risk_level,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if scope_source_id is not UNSET:
            field_dict["scope_source_id"] = scope_source_id
        if scope_signal_set is not UNSET:
            field_dict["scope_signal_set"] = scope_signal_set

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        expires_at = isoparse(d.pop("expires_at"))

        scope_source_type = CreateShadowExceptionRequestScopeSourceType(d.pop("scope_source_type"))

        scope_risk_level = CreateShadowExceptionRequestScopeRiskLevel(d.pop("scope_risk_level"))

        reason = d.pop("reason", UNSET)

        scope_source_id = d.pop("scope_source_id", UNSET)

        scope_signal_set = cast(List[str], d.pop("scope_signal_set", UNSET))

        create_shadow_exception_request = cls(
            expires_at=expires_at,
            scope_source_type=scope_source_type,
            scope_risk_level=scope_risk_level,
            reason=reason,
            scope_source_id=scope_source_id,
            scope_signal_set=scope_signal_set,
        )

        create_shadow_exception_request.additional_properties = d
        return create_shadow_exception_request

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
