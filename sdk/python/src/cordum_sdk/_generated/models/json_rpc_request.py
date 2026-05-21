from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.json_rpc_request_jsonrpc import JsonRpcRequestJsonrpc
from ..types import UNSET, Unset
from typing import cast
from typing import cast, Union
from typing import Dict
from typing import Union

if TYPE_CHECKING:
    from ..models.json_rpc_request_params import JsonRpcRequestParams


T = TypeVar("T", bound="JsonRpcRequest")


@_attrs_define
class JsonRpcRequest:
    """
    Attributes:
        jsonrpc (JsonRpcRequestJsonrpc):
        method (str):
        id (Union[Unset, int, str]):
        params (Union[Unset, JsonRpcRequestParams]):
    """

    jsonrpc: JsonRpcRequestJsonrpc
    method: str
    id: Union[Unset, int, str] = UNSET
    params: Union[Unset, "JsonRpcRequestParams"] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.json_rpc_request_params import JsonRpcRequestParams

        jsonrpc = self.jsonrpc.value

        method = self.method

        id: Union[Unset, int, str]
        if isinstance(self.id, Unset):
            id = UNSET
        else:
            id = self.id

        params: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.params, Unset):
            params = self.params.to_dict()

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "jsonrpc": jsonrpc,
                "method": method,
            }
        )
        if id is not UNSET:
            field_dict["id"] = id
        if params is not UNSET:
            field_dict["params"] = params

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.json_rpc_request_params import JsonRpcRequestParams

        d = src_dict.copy()
        jsonrpc = JsonRpcRequestJsonrpc(d.pop("jsonrpc"))

        method = d.pop("method")

        def _parse_id(data: object) -> Union[Unset, int, str]:
            if isinstance(data, Unset):
                return data
            return cast(Union[Unset, int, str], data)

        id = _parse_id(d.pop("id", UNSET))

        _params = d.pop("params", UNSET)
        params: Union[Unset, JsonRpcRequestParams]
        if isinstance(_params, Unset):
            params = UNSET
        else:
            params = JsonRpcRequestParams.from_dict(_params)

        json_rpc_request = cls(
            jsonrpc=jsonrpc,
            method=method,
            id=id,
            params=params,
        )

        json_rpc_request.additional_properties = d
        return json_rpc_request

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
