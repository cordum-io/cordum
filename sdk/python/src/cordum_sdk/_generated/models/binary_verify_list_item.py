from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.binary_verify_event_event import BinaryVerifyEventEvent
from ..models.binary_verify_event_sig_scheme import BinaryVerifyEventSigScheme
from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import Union
import datetime


T = TypeVar("T", bound="BinaryVerifyListItem")


@_attrs_define
class BinaryVerifyListItem:
    """One row in the binary-verify list response. Carries the original
    `BinaryVerifyEvent` shape plus the server-side audit-row metadata
    captured at ingest time.

        Attributes:
            event (BinaryVerifyEventEvent): Outcome kind. Maps to SIEMEvent.EventType.
            hash_ (str): SHA-256 of the verified binary, lowercase hex.
            path (str): Manifest-relative basename of the verified binary. Absolute
                paths, drive-rooted (Windows) paths, and any segment
                containing `..` are rejected.
            sig_scheme (BinaryVerifyEventSigScheme): Signing scheme that produced the verified signature.
            fingerprint (str): Empty when sig_scheme is codesign/authenticode/dev, or a
                40-char uppercase-hex GPG key fingerprint when sig_scheme
                is gpg. When non-empty, must match `^[A-F0-9]{40}$`.
            reason (str): Human-readable explanation of the outcome. Empty on success;
                install-script controlled failure text on failure. Capped to
                512 chars defensively (operator relays may splice in raw
                gpg stderr).
            exit_code (int): Exit code of the verifier. MUST be 0 when event is
                `binary-verify-ok` and non-zero when event is
                `binary-verify-fail`.
            timestamp (datetime.datetime): Server-side ingest timestamp (UTC).
            tenant_id (str): Tenant that owns the event.
            endpoint (Union[Unset, str]): Operator-supplied label captured at ingest time. Empty when
                the ingester did not provide one.
    """

    event: BinaryVerifyEventEvent
    hash_: str
    path: str
    sig_scheme: BinaryVerifyEventSigScheme
    fingerprint: str
    reason: str
    exit_code: int
    timestamp: datetime.datetime
    tenant_id: str
    endpoint: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        event = self.event.value

        hash_ = self.hash_

        path = self.path

        sig_scheme = self.sig_scheme.value

        fingerprint = self.fingerprint

        reason = self.reason

        exit_code = self.exit_code

        timestamp = self.timestamp.isoformat()

        tenant_id = self.tenant_id

        endpoint = self.endpoint

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "event": event,
                "hash": hash_,
                "path": path,
                "sig_scheme": sig_scheme,
                "fingerprint": fingerprint,
                "reason": reason,
                "exit_code": exit_code,
                "timestamp": timestamp,
                "tenant_id": tenant_id,
            }
        )
        if endpoint is not UNSET:
            field_dict["endpoint"] = endpoint

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        event = BinaryVerifyEventEvent(d.pop("event"))

        hash_ = d.pop("hash")

        path = d.pop("path")

        sig_scheme = BinaryVerifyEventSigScheme(d.pop("sig_scheme"))

        fingerprint = d.pop("fingerprint")

        reason = d.pop("reason")

        exit_code = d.pop("exit_code")

        timestamp = isoparse(d.pop("timestamp"))

        tenant_id = d.pop("tenant_id")

        endpoint = d.pop("endpoint", UNSET)

        binary_verify_list_item = cls(
            event=event,
            hash_=hash_,
            path=path,
            sig_scheme=sig_scheme,
            fingerprint=fingerprint,
            reason=reason,
            exit_code=exit_code,
            timestamp=timestamp,
            tenant_id=tenant_id,
            endpoint=endpoint,
        )

        binary_verify_list_item.additional_properties = d
        return binary_verify_list_item

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
