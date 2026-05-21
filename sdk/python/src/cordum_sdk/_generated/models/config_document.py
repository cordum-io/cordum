from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.config_document_scope import ConfigDocumentScope
from ..types import UNSET, Unset
from typing import cast
from typing import Dict
from typing import Union

if TYPE_CHECKING:
    from ..models.config_document_data import ConfigDocumentData


T = TypeVar("T", bound="ConfigDocument")


@_attrs_define
class ConfigDocument:
    """
    Attributes:
        scope (Union[Unset, ConfigDocumentScope]):
        scope_id (Union[Unset, str]):
        data (Union[Unset, ConfigDocumentData]): Configuration key-value pairs
    """

    scope: Union[Unset, ConfigDocumentScope] = UNSET
    scope_id: Union[Unset, str] = UNSET
    data: Union[Unset, "ConfigDocumentData"] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.config_document_data import ConfigDocumentData

        scope: Union[Unset, str] = UNSET
        if not isinstance(self.scope, Unset):
            scope = self.scope.value

        scope_id = self.scope_id

        data: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.data, Unset):
            data = self.data.to_dict()

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if scope is not UNSET:
            field_dict["scope"] = scope
        if scope_id is not UNSET:
            field_dict["scope_id"] = scope_id
        if data is not UNSET:
            field_dict["data"] = data

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.config_document_data import ConfigDocumentData

        d = src_dict.copy()
        _scope = d.pop("scope", UNSET)
        scope: Union[Unset, ConfigDocumentScope]
        if isinstance(_scope, Unset):
            scope = UNSET
        else:
            scope = ConfigDocumentScope(_scope)

        scope_id = d.pop("scope_id", UNSET)

        _data = d.pop("data", UNSET)
        data: Union[Unset, ConfigDocumentData]
        if isinstance(_data, Unset):
            data = UNSET
        else:
            data = ConfigDocumentData.from_dict(_data)

        config_document = cls(
            scope=scope,
            scope_id=scope_id,
            data=data,
        )

        config_document.additional_properties = d
        return config_document

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
