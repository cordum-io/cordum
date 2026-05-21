from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.pack_record_status import PackRecordStatus
from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import cast, Union
from typing import Dict
from typing import Union
import datetime

if TYPE_CHECKING:
    from ..models.installed_pack_verification import InstalledPackVerification


T = TypeVar("T", bound="PackRecord")


@_attrs_define
class PackRecord:
    """
    Attributes:
        id (Union[Unset, str]):
        name (Union[Unset, str]):
        version (Union[Unset, str]):
        status (Union[Unset, PackRecordStatus]):
        description (Union[None, Unset, str]):
        author (Union[None, Unset, str]):
        installed_at (Union[Unset, datetime.datetime]):
        updated_at (Union[Unset, datetime.datetime]):
        verification (Union[Unset, InstalledPackVerification]): Pack-signature verification outcome computed by the
            gateway at
            install time. Never client-supplied — the gateway discards any
            `verification` field on the install payload and computes its own.
            Pre-existing installs default to {signed: false} when this object
            is absent.
    """

    id: Union[Unset, str] = UNSET
    name: Union[Unset, str] = UNSET
    version: Union[Unset, str] = UNSET
    status: Union[Unset, PackRecordStatus] = UNSET
    description: Union[None, Unset, str] = UNSET
    author: Union[None, Unset, str] = UNSET
    installed_at: Union[Unset, datetime.datetime] = UNSET
    updated_at: Union[Unset, datetime.datetime] = UNSET
    verification: Union[Unset, "InstalledPackVerification"] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.installed_pack_verification import InstalledPackVerification

        id = self.id

        name = self.name

        version = self.version

        status: Union[Unset, str] = UNSET
        if not isinstance(self.status, Unset):
            status = self.status.value

        description: Union[None, Unset, str]
        if isinstance(self.description, Unset):
            description = UNSET
        else:
            description = self.description

        author: Union[None, Unset, str]
        if isinstance(self.author, Unset):
            author = UNSET
        else:
            author = self.author

        installed_at: Union[Unset, str] = UNSET
        if not isinstance(self.installed_at, Unset):
            installed_at = self.installed_at.isoformat()

        updated_at: Union[Unset, str] = UNSET
        if not isinstance(self.updated_at, Unset):
            updated_at = self.updated_at.isoformat()

        verification: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.verification, Unset):
            verification = self.verification.to_dict()

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if id is not UNSET:
            field_dict["id"] = id
        if name is not UNSET:
            field_dict["name"] = name
        if version is not UNSET:
            field_dict["version"] = version
        if status is not UNSET:
            field_dict["status"] = status
        if description is not UNSET:
            field_dict["description"] = description
        if author is not UNSET:
            field_dict["author"] = author
        if installed_at is not UNSET:
            field_dict["installed_at"] = installed_at
        if updated_at is not UNSET:
            field_dict["updated_at"] = updated_at
        if verification is not UNSET:
            field_dict["verification"] = verification

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.installed_pack_verification import InstalledPackVerification

        d = src_dict.copy()
        id = d.pop("id", UNSET)

        name = d.pop("name", UNSET)

        version = d.pop("version", UNSET)

        _status = d.pop("status", UNSET)
        status: Union[Unset, PackRecordStatus]
        if isinstance(_status, Unset):
            status = UNSET
        else:
            status = PackRecordStatus(_status)

        def _parse_description(data: object) -> Union[None, Unset, str]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, str], data)

        description = _parse_description(d.pop("description", UNSET))

        def _parse_author(data: object) -> Union[None, Unset, str]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, str], data)

        author = _parse_author(d.pop("author", UNSET))

        _installed_at = d.pop("installed_at", UNSET)
        installed_at: Union[Unset, datetime.datetime]
        if isinstance(_installed_at, Unset):
            installed_at = UNSET
        else:
            installed_at = isoparse(_installed_at)

        _updated_at = d.pop("updated_at", UNSET)
        updated_at: Union[Unset, datetime.datetime]
        if isinstance(_updated_at, Unset):
            updated_at = UNSET
        else:
            updated_at = isoparse(_updated_at)

        _verification = d.pop("verification", UNSET)
        verification: Union[Unset, InstalledPackVerification]
        if isinstance(_verification, Unset):
            verification = UNSET
        else:
            verification = InstalledPackVerification.from_dict(_verification)

        pack_record = cls(
            id=id,
            name=name,
            version=version,
            status=status,
            description=description,
            author=author,
            installed_at=installed_at,
            updated_at=updated_at,
            verification=verification,
        )

        pack_record.additional_properties = d
        return pack_record

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
