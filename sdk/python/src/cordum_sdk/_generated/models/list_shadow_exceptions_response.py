from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast
from typing import cast, List
from typing import Dict
from typing import Union

if TYPE_CHECKING:
    from ..models.shadow_exception import ShadowException


T = TypeVar("T", bound="ListShadowExceptionsResponse")


@_attrs_define
class ListShadowExceptionsResponse:
    """
    Attributes:
        exceptions (List['ShadowException']):
        next_cursor (Union[Unset, str]):
    """

    exceptions: List["ShadowException"]
    next_cursor: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.shadow_exception import ShadowException

        exceptions = []
        for exceptions_item_data in self.exceptions:
            exceptions_item = exceptions_item_data.to_dict()
            exceptions.append(exceptions_item)

        next_cursor = self.next_cursor

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "exceptions": exceptions,
            }
        )
        if next_cursor is not UNSET:
            field_dict["next_cursor"] = next_cursor

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.shadow_exception import ShadowException

        d = src_dict.copy()
        exceptions = []
        _exceptions = d.pop("exceptions")
        for exceptions_item_data in _exceptions:
            exceptions_item = ShadowException.from_dict(exceptions_item_data)

            exceptions.append(exceptions_item)

        next_cursor = d.pop("next_cursor", UNSET)

        list_shadow_exceptions_response = cls(
            exceptions=exceptions,
            next_cursor=next_cursor,
        )

        list_shadow_exceptions_response.additional_properties = d
        return list_shadow_exceptions_response

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
