from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset


T = TypeVar("T", bound="RevokeWorkerSessionResponse200")


@_attrs_define
class RevokeWorkerSessionResponse200:
    """
    Attributes:
        worker_id (str):
        tenant (str):
        revoked (bool):
    """

    worker_id: str
    tenant: str
    revoked: bool
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        worker_id = self.worker_id

        tenant = self.tenant

        revoked = self.revoked

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "worker_id": worker_id,
                "tenant": tenant,
                "revoked": revoked,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        worker_id = d.pop("worker_id")

        tenant = d.pop("tenant")

        revoked = d.pop("revoked")

        revoke_worker_session_response_200 = cls(
            worker_id=worker_id,
            tenant=tenant,
            revoked=revoked,
        )

        revoke_worker_session_response_200.additional_properties = d
        return revoke_worker_session_response_200

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
