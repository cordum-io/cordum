from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.json_rpc_response_jsonrpc import JsonRpcResponseJsonrpc
from ..types import UNSET, Unset
from typing import cast
from typing import cast, Union
from typing import Dict
from typing import Union

if TYPE_CHECKING:
    from ..models.json_rpc_response_error_type_0 import JsonRpcResponseErrorType0
    from ..models.json_rpc_response_result_type_0 import JsonRpcResponseResultType0


T = TypeVar("T", bound="JsonRpcResponse")


@_attrs_define
class JsonRpcResponse:
    """
    Attributes:
        jsonrpc (Union[Unset, JsonRpcResponseJsonrpc]):
        id (Union[Unset, int, str]):
        result (Union['JsonRpcResponseResultType0', None, Unset]):
        error (Union['JsonRpcResponseErrorType0', None, Unset]):
    """

    jsonrpc: Union[Unset, JsonRpcResponseJsonrpc] = UNSET
    id: Union[Unset, int, str] = UNSET
    result: Union["JsonRpcResponseResultType0", None, Unset] = UNSET
    error: Union["JsonRpcResponseErrorType0", None, Unset] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.json_rpc_response_error_type_0 import JsonRpcResponseErrorType0
        from ..models.json_rpc_response_result_type_0 import JsonRpcResponseResultType0

        jsonrpc: Union[Unset, str] = UNSET
        if not isinstance(self.jsonrpc, Unset):
            jsonrpc = self.jsonrpc.value

        id: Union[Unset, int, str]
        if isinstance(self.id, Unset):
            id = UNSET
        else:
            id = self.id

        result: Union[Dict[str, Any], None, Unset]
        if isinstance(self.result, Unset):
            result = UNSET
        elif isinstance(self.result, JsonRpcResponseResultType0):
            result = self.result.to_dict()
        else:
            result = self.result

        error: Union[Dict[str, Any], None, Unset]
        if isinstance(self.error, Unset):
            error = UNSET
        elif isinstance(self.error, JsonRpcResponseErrorType0):
            error = self.error.to_dict()
        else:
            error = self.error

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if jsonrpc is not UNSET:
            field_dict["jsonrpc"] = jsonrpc
        if id is not UNSET:
            field_dict["id"] = id
        if result is not UNSET:
            field_dict["result"] = result
        if error is not UNSET:
            field_dict["error"] = error

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.json_rpc_response_error_type_0 import JsonRpcResponseErrorType0
        from ..models.json_rpc_response_result_type_0 import JsonRpcResponseResultType0

        d = src_dict.copy()
        _jsonrpc = d.pop("jsonrpc", UNSET)
        jsonrpc: Union[Unset, JsonRpcResponseJsonrpc]
        if isinstance(_jsonrpc, Unset):
            jsonrpc = UNSET
        else:
            jsonrpc = JsonRpcResponseJsonrpc(_jsonrpc)

        def _parse_id(data: object) -> Union[Unset, int, str]:
            if isinstance(data, Unset):
                return data
            return cast(Union[Unset, int, str], data)

        id = _parse_id(d.pop("id", UNSET))

        def _parse_result(data: object) -> Union["JsonRpcResponseResultType0", None, Unset]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                result_type_0 = JsonRpcResponseResultType0.from_dict(data)

                return result_type_0
            except:  # noqa: E722
                pass
            return cast(Union["JsonRpcResponseResultType0", None, Unset], data)

        result = _parse_result(d.pop("result", UNSET))

        def _parse_error(data: object) -> Union["JsonRpcResponseErrorType0", None, Unset]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                error_type_0 = JsonRpcResponseErrorType0.from_dict(data)

                return error_type_0
            except:  # noqa: E722
                pass
            return cast(Union["JsonRpcResponseErrorType0", None, Unset], data)

        error = _parse_error(d.pop("error", UNSET))

        json_rpc_response = cls(
            jsonrpc=jsonrpc,
            id=id,
            result=result,
            error=error,
        )

        json_rpc_response.additional_properties = d
        return json_rpc_response

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
