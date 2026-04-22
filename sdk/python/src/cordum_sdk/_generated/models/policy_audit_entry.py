from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import cast, Union
from typing import Union
import datetime






T = TypeVar("T", bound="PolicyAuditEntry")


@_attrs_define
class PolicyAuditEntry:
    """ 
        Attributes:
            id (Union[Unset, str]):
            action (Union[Unset, str]):
            author (Union[Unset, str]):
            timestamp (Union[Unset, datetime.datetime]):
            bundle_id (Union[None, Unset, str]):
            snapshot_id (Union[None, Unset, str]):
            message (Union[None, Unset, str]):
     """

    id: Union[Unset, str] = UNSET
    action: Union[Unset, str] = UNSET
    author: Union[Unset, str] = UNSET
    timestamp: Union[Unset, datetime.datetime] = UNSET
    bundle_id: Union[None, Unset, str] = UNSET
    snapshot_id: Union[None, Unset, str] = UNSET
    message: Union[None, Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)


    def to_dict(self) -> Dict[str, Any]:
        id = self.id

        action = self.action

        author = self.author

        timestamp: Union[Unset, str] = UNSET
        if not isinstance(self.timestamp, Unset):
            timestamp = self.timestamp.isoformat()

        bundle_id: Union[None, Unset, str]
        if isinstance(self.bundle_id, Unset):
            bundle_id = UNSET
        else:
            bundle_id = self.bundle_id

        snapshot_id: Union[None, Unset, str]
        if isinstance(self.snapshot_id, Unset):
            snapshot_id = UNSET
        else:
            snapshot_id = self.snapshot_id

        message: Union[None, Unset, str]
        if isinstance(self.message, Unset):
            message = UNSET
        else:
            message = self.message


        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({
        })
        if id is not UNSET:
            field_dict["id"] = id
        if action is not UNSET:
            field_dict["action"] = action
        if author is not UNSET:
            field_dict["author"] = author
        if timestamp is not UNSET:
            field_dict["timestamp"] = timestamp
        if bundle_id is not UNSET:
            field_dict["bundle_id"] = bundle_id
        if snapshot_id is not UNSET:
            field_dict["snapshot_id"] = snapshot_id
        if message is not UNSET:
            field_dict["message"] = message

        return field_dict



    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        id = d.pop("id", UNSET)

        action = d.pop("action", UNSET)

        author = d.pop("author", UNSET)

        _timestamp = d.pop("timestamp", UNSET)
        timestamp: Union[Unset, datetime.datetime]
        if isinstance(_timestamp,  Unset):
            timestamp = UNSET
        else:
            timestamp = isoparse(_timestamp)




        def _parse_bundle_id(data: object) -> Union[None, Unset, str]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, str], data)

        bundle_id = _parse_bundle_id(d.pop("bundle_id", UNSET))


        def _parse_snapshot_id(data: object) -> Union[None, Unset, str]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, str], data)

        snapshot_id = _parse_snapshot_id(d.pop("snapshot_id", UNSET))


        def _parse_message(data: object) -> Union[None, Unset, str]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(Union[None, Unset, str], data)

        message = _parse_message(d.pop("message", UNSET))


        policy_audit_entry = cls(
            id=id,
            action=action,
            author=author,
            timestamp=timestamp,
            bundle_id=bundle_id,
            snapshot_id=snapshot_id,
            message=message,
        )


        policy_audit_entry.additional_properties = d
        return policy_audit_entry

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
