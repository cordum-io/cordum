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
    from ..models.policy_check_request import PolicyCheckRequest


T = TypeVar("T", bound="SimulatePolicyBundleBody")


@_attrs_define
class SimulatePolicyBundleBody:
    """
    Attributes:
        request (Union[Unset, PolicyCheckRequest]):
        content (Union[Unset, str]): Optional override bundle YAML content
    """

    request: Union[Unset, "PolicyCheckRequest"] = UNSET
    content: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.policy_check_request import PolicyCheckRequest

        request: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.request, Unset):
            request = self.request.to_dict()

        content = self.content

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if request is not UNSET:
            field_dict["request"] = request
        if content is not UNSET:
            field_dict["content"] = content

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.policy_check_request import PolicyCheckRequest

        d = src_dict.copy()
        _request = d.pop("request", UNSET)
        request: Union[Unset, PolicyCheckRequest]
        if isinstance(_request, Unset):
            request = UNSET
        else:
            request = PolicyCheckRequest.from_dict(_request)

        content = d.pop("content", UNSET)

        simulate_policy_bundle_body = cls(
            request=request,
            content=content,
        )

        simulate_policy_bundle_body.additional_properties = d
        return simulate_policy_bundle_body

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
