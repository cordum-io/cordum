from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.installed_pack_verification_signature_algorithm import (
    InstalledPackVerificationSignatureAlgorithm,
)
from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import cast, List
from typing import Union
import datetime


T = TypeVar("T", bound="InstalledPackVerification")


@_attrs_define
class InstalledPackVerification:
    """Pack-signature verification outcome computed by the gateway at
    install time. Never client-supplied — the gateway discards any
    `verification` field on the install payload and computes its own.
    Pre-existing installs default to {signed: false} when this object
    is absent.

        Attributes:
            signed (bool): True when the gateway successfully verified the pack against
                a trusted publisher key at install time.
            publisher_id (Union[Unset, str]): Publisher identifier derived from the trusted key's metadata.
            kid (Union[Unset, str]): Key id of the Ed25519 public key that verified the pack.
            verified_at (Union[Unset, datetime.datetime]): RFC3339 timestamp of the server-side verification.
            has_cordum_counter_sig (Union[Unset, bool]): True when a valid Cordum counter-signature envelope
                (pack.yaml.sig.cordum) is present and verified against the
                embedded Cordum counter-signing key.
            signature_algorithm (Union[Unset, InstalledPackVerificationSignatureAlgorithm]): Algorithm advertised by the
                verified envelope. Always ed25519 today.
            pack_signature_version (Union[Unset, int]): Canonical manifest version that was signed.
            warnings (Union[Unset, List[str]]): Non-fatal warnings the gateway wants to surface alongside the
                verification outcome (for example, "pack accepted unsigned —
                gateway strict mode disabled").
    """

    signed: bool
    publisher_id: Union[Unset, str] = UNSET
    kid: Union[Unset, str] = UNSET
    verified_at: Union[Unset, datetime.datetime] = UNSET
    has_cordum_counter_sig: Union[Unset, bool] = UNSET
    signature_algorithm: Union[Unset, InstalledPackVerificationSignatureAlgorithm] = UNSET
    pack_signature_version: Union[Unset, int] = UNSET
    warnings: Union[Unset, List[str]] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        signed = self.signed

        publisher_id = self.publisher_id

        kid = self.kid

        verified_at: Union[Unset, str] = UNSET
        if not isinstance(self.verified_at, Unset):
            verified_at = self.verified_at.isoformat()

        has_cordum_counter_sig = self.has_cordum_counter_sig

        signature_algorithm: Union[Unset, str] = UNSET
        if not isinstance(self.signature_algorithm, Unset):
            signature_algorithm = self.signature_algorithm.value

        pack_signature_version = self.pack_signature_version

        warnings: Union[Unset, List[str]] = UNSET
        if not isinstance(self.warnings, Unset):
            warnings = self.warnings

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "signed": signed,
            }
        )
        if publisher_id is not UNSET:
            field_dict["publisher_id"] = publisher_id
        if kid is not UNSET:
            field_dict["kid"] = kid
        if verified_at is not UNSET:
            field_dict["verified_at"] = verified_at
        if has_cordum_counter_sig is not UNSET:
            field_dict["has_cordum_counter_sig"] = has_cordum_counter_sig
        if signature_algorithm is not UNSET:
            field_dict["signature_algorithm"] = signature_algorithm
        if pack_signature_version is not UNSET:
            field_dict["pack_signature_version"] = pack_signature_version
        if warnings is not UNSET:
            field_dict["warnings"] = warnings

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        signed = d.pop("signed")

        publisher_id = d.pop("publisher_id", UNSET)

        kid = d.pop("kid", UNSET)

        _verified_at = d.pop("verified_at", UNSET)
        verified_at: Union[Unset, datetime.datetime]
        if isinstance(_verified_at, Unset):
            verified_at = UNSET
        else:
            verified_at = isoparse(_verified_at)

        has_cordum_counter_sig = d.pop("has_cordum_counter_sig", UNSET)

        _signature_algorithm = d.pop("signature_algorithm", UNSET)
        signature_algorithm: Union[Unset, InstalledPackVerificationSignatureAlgorithm]
        if isinstance(_signature_algorithm, Unset):
            signature_algorithm = UNSET
        else:
            signature_algorithm = InstalledPackVerificationSignatureAlgorithm(_signature_algorithm)

        pack_signature_version = d.pop("pack_signature_version", UNSET)

        warnings = cast(List[str], d.pop("warnings", UNSET))

        installed_pack_verification = cls(
            signed=signed,
            publisher_id=publisher_id,
            kid=kid,
            verified_at=verified_at,
            has_cordum_counter_sig=has_cordum_counter_sig,
            signature_algorithm=signature_algorithm,
            pack_signature_version=pack_signature_version,
            warnings=warnings,
        )

        installed_pack_verification.additional_properties = d
        return installed_pack_verification

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
