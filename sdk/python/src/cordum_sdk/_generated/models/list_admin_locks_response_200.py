from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from typing import cast
from typing import cast, List
from typing import Dict

if TYPE_CHECKING:
    from ..models.admin_lock import AdminLock


T = TypeVar("T", bound="ListAdminLocksResponse200")


@_attrs_define
class ListAdminLocksResponse200:
    """
    Attributes:
        locks (List['AdminLock']):
    """

    locks: List["AdminLock"]
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.admin_lock import AdminLock

        locks = []
        for locks_item_data in self.locks:
            locks_item = locks_item_data.to_dict()
            locks.append(locks_item)

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "locks": locks,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.admin_lock import AdminLock

        d = src_dict.copy()
        locks = []
        _locks = d.pop("locks")
        for locks_item_data in _locks:
            locks_item = AdminLock.from_dict(locks_item_data)

            locks.append(locks_item)

        list_admin_locks_response_200 = cls(
            locks=locks,
        )

        list_admin_locks_response_200.additional_properties = d
        return list_admin_locks_response_200

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
