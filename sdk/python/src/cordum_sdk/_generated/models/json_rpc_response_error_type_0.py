from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast
from typing import cast, Union
from typing import Dict
from typing import Union

if TYPE_CHECKING:
    from ..models.json_rpc_response_error_type_0_data_type_0 import (
        JsonRpcResponseErrorType0DataType0,
    )


T = TypeVar("T", bound="JsonRpcResponseErrorType0")


@_attrs_define
class JsonRpcResponseErrorType0:
    """
    Attributes:
        code (Union[Unset, int]):
        message (Union[Unset, str]):
        data (Union['JsonRpcResponseErrorType0DataType0', None, Unset]):
    """

    code: Union[Unset, int] = UNSET
    message: Union[Unset, str] = UNSET
    data: Union["JsonRpcResponseErrorType0DataType0", None, Unset] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.json_rpc_response_error_type_0_data_type_0 import (
            JsonRpcResponseErrorType0DataType0,
        )

        code = self.code

        message = self.message

        data: Union[Dict[str, Any], None, Unset]
        if isinstance(self.data, Unset):
            data = UNSET
        elif isinstance(self.data, JsonRpcResponseErrorType0DataType0):
            data = self.data.to_dict()
        else:
            data = self.data

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if code is not UNSET:
            field_dict["code"] = code
        if message is not UNSET:
            field_dict["message"] = message
        if data is not UNSET:
            field_dict["data"] = data

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.json_rpc_response_error_type_0_data_type_0 import (
            JsonRpcResponseErrorType0DataType0,
        )

        d = src_dict.copy()
        code = d.pop("code", UNSET)

        message = d.pop("message", UNSET)

        def _parse_data(data: object) -> Union["JsonRpcResponseErrorType0DataType0", None, Unset]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                data_type_0 = JsonRpcResponseErrorType0DataType0.from_dict(data)

                return data_type_0
            except:  # noqa: E722
                pass
            return cast(Union["JsonRpcResponseErrorType0DataType0", None, Unset], data)

        data = _parse_data(d.pop("data", UNSET))

        json_rpc_response_error_type_0 = cls(
            code=code,
            message=message,
            data=data,
        )

        json_rpc_response_error_type_0.additional_properties = d
        return json_rpc_response_error_type_0

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
