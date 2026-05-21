from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.shadow_exception_scope_risk_level import ShadowExceptionScopeRiskLevel
from ..models.shadow_exception_scope_source_type import ShadowExceptionScopeSourceType
from ..models.shadow_exception_status import ShadowExceptionStatus
from ..models.shadow_exception_step_up_factor import ShadowExceptionStepUpFactor
from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import cast, List
from typing import Union
import datetime


T = TypeVar("T", bound="ShadowException")


@_attrs_define
class ShadowException:
    """Operator-defined exception declaration (EDGE-143.6 / §10.3). Suppresses
    future ShadowAgent findings matching its scope predicate. The
    step_up_factor records which auth tier satisfied the Q8 step-up gate
    at creation time so SIEM rules can pivot on authority-at-time-of-action.

        Attributes:
            exception_id (str): Opaque id with `shadow_exc_` prefix.
            tenant_id (str):
            created_by (str): Authenticated principal at create time. Not trusted from the wire body.
            created_at (datetime.datetime):
            expires_at (datetime.datetime): Maximum 90 days from creation (longer requires re-affirmation per §10.3).
            scope_source_type (ShadowExceptionScopeSourceType):
            scope_risk_level (ShadowExceptionScopeRiskLevel):
            status (ShadowExceptionStatus):
            step_up_factor (ShadowExceptionStepUpFactor): Auth tier that satisfied the Q8 step-up gate at create time.
                "signed_admin_token" when the legacy admin role matched;
                "mfa_recent" when the explicit shadow.exception.high_risk
                permission matched; "none" when the gate was not required.
            reason (Union[Unset, str]): Free-text operator rationale, up to 512 bytes.
            scope_source_id (Union[Unset, str]): Optional detector instance id; further narrows scope.
            scope_signal_set (Union[Unset, List[str]]): At most 16 detector signal names; any-of overlap with a finding's
                signal_set satisfies the predicate.
            revoked_by (Union[Unset, str]):
            revoked_at (Union[Unset, datetime.datetime]):
            revocation_reason (Union[Unset, str]):
    """

    exception_id: str
    tenant_id: str
    created_by: str
    created_at: datetime.datetime
    expires_at: datetime.datetime
    scope_source_type: ShadowExceptionScopeSourceType
    scope_risk_level: ShadowExceptionScopeRiskLevel
    status: ShadowExceptionStatus
    step_up_factor: ShadowExceptionStepUpFactor
    reason: Union[Unset, str] = UNSET
    scope_source_id: Union[Unset, str] = UNSET
    scope_signal_set: Union[Unset, List[str]] = UNSET
    revoked_by: Union[Unset, str] = UNSET
    revoked_at: Union[Unset, datetime.datetime] = UNSET
    revocation_reason: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        exception_id = self.exception_id

        tenant_id = self.tenant_id

        created_by = self.created_by

        created_at = self.created_at.isoformat()

        expires_at = self.expires_at.isoformat()

        scope_source_type = self.scope_source_type.value

        scope_risk_level = self.scope_risk_level.value

        status = self.status.value

        step_up_factor = self.step_up_factor.value

        reason = self.reason

        scope_source_id = self.scope_source_id

        scope_signal_set: Union[Unset, List[str]] = UNSET
        if not isinstance(self.scope_signal_set, Unset):
            scope_signal_set = self.scope_signal_set

        revoked_by = self.revoked_by

        revoked_at: Union[Unset, str] = UNSET
        if not isinstance(self.revoked_at, Unset):
            revoked_at = self.revoked_at.isoformat()

        revocation_reason = self.revocation_reason

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "exception_id": exception_id,
                "tenant_id": tenant_id,
                "created_by": created_by,
                "created_at": created_at,
                "expires_at": expires_at,
                "scope_source_type": scope_source_type,
                "scope_risk_level": scope_risk_level,
                "status": status,
                "step_up_factor": step_up_factor,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if scope_source_id is not UNSET:
            field_dict["scope_source_id"] = scope_source_id
        if scope_signal_set is not UNSET:
            field_dict["scope_signal_set"] = scope_signal_set
        if revoked_by is not UNSET:
            field_dict["revoked_by"] = revoked_by
        if revoked_at is not UNSET:
            field_dict["revoked_at"] = revoked_at
        if revocation_reason is not UNSET:
            field_dict["revocation_reason"] = revocation_reason

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        exception_id = d.pop("exception_id")

        tenant_id = d.pop("tenant_id")

        created_by = d.pop("created_by")

        created_at = isoparse(d.pop("created_at"))

        expires_at = isoparse(d.pop("expires_at"))

        scope_source_type = ShadowExceptionScopeSourceType(d.pop("scope_source_type"))

        scope_risk_level = ShadowExceptionScopeRiskLevel(d.pop("scope_risk_level"))

        status = ShadowExceptionStatus(d.pop("status"))

        step_up_factor = ShadowExceptionStepUpFactor(d.pop("step_up_factor"))

        reason = d.pop("reason", UNSET)

        scope_source_id = d.pop("scope_source_id", UNSET)

        scope_signal_set = cast(List[str], d.pop("scope_signal_set", UNSET))

        revoked_by = d.pop("revoked_by", UNSET)

        _revoked_at = d.pop("revoked_at", UNSET)
        revoked_at: Union[Unset, datetime.datetime]
        if isinstance(_revoked_at, Unset):
            revoked_at = UNSET
        else:
            revoked_at = isoparse(_revoked_at)

        revocation_reason = d.pop("revocation_reason", UNSET)

        shadow_exception = cls(
            exception_id=exception_id,
            tenant_id=tenant_id,
            created_by=created_by,
            created_at=created_at,
            expires_at=expires_at,
            scope_source_type=scope_source_type,
            scope_risk_level=scope_risk_level,
            status=status,
            step_up_factor=step_up_factor,
            reason=reason,
            scope_source_id=scope_source_id,
            scope_signal_set=scope_signal_set,
            revoked_by=revoked_by,
            revoked_at=revoked_at,
            revocation_reason=revocation_reason,
        )

        shadow_exception.additional_properties = d
        return shadow_exception

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
