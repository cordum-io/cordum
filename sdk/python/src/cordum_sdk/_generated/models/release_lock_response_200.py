from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast
from typing import Dict
from typing import Union

if TYPE_CHECKING:
    from ..models.lock import Lock


T = TypeVar("T", bound="ReleaseLockResponse200")


@_attrs_define
class ReleaseLockResponse200:
    """
    Attributes:
        lock (Union[Unset, Lock]):
        released (Union[Unset, bool]):
    """

    lock: Union[Unset, "Lock"] = UNSET
    released: Union[Unset, bool] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.lock import Lock

        lock: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.lock, Unset):
            lock = self.lock.to_dict()

        released = self.released

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if lock is not UNSET:
            field_dict["lock"] = lock
        if released is not UNSET:
            field_dict["released"] = released

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.lock import Lock

        d = src_dict.copy()
        _lock = d.pop("lock", UNSET)
        lock: Union[Unset, Lock]
        if isinstance(_lock, Unset):
            lock = UNSET
        else:
            lock = Lock.from_dict(_lock)

        released = d.pop("released", UNSET)

        release_lock_response_200 = cls(
            lock=lock,
            released=released,
        )

        release_lock_response_200.additional_properties = d
        return release_lock_response_200

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
