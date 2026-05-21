from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import Union


T = TypeVar("T", bound="EvalRunRequest")


@_attrs_define
class EvalRunRequest:
    """
    Attributes:
        use_current_policy (Union[Unset, bool]):
        candidate_bundle_id (Union[Unset, str]):
        candidate_content (Union[Unset, str]):
        max_entries (Union[Unset, int]):
    """

    use_current_policy: Union[Unset, bool] = UNSET
    candidate_bundle_id: Union[Unset, str] = UNSET
    candidate_content: Union[Unset, str] = UNSET
    max_entries: Union[Unset, int] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        use_current_policy = self.use_current_policy

        candidate_bundle_id = self.candidate_bundle_id

        candidate_content = self.candidate_content

        max_entries = self.max_entries

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if use_current_policy is not UNSET:
            field_dict["use_current_policy"] = use_current_policy
        if candidate_bundle_id is not UNSET:
            field_dict["candidate_bundle_id"] = candidate_bundle_id
        if candidate_content is not UNSET:
            field_dict["candidate_content"] = candidate_content
        if max_entries is not UNSET:
            field_dict["max_entries"] = max_entries

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        use_current_policy = d.pop("use_current_policy", UNSET)

        candidate_bundle_id = d.pop("candidate_bundle_id", UNSET)

        candidate_content = d.pop("candidate_content", UNSET)

        max_entries = d.pop("max_entries", UNSET)

        eval_run_request = cls(
            use_current_policy=use_current_policy,
            candidate_bundle_id=candidate_bundle_id,
            candidate_content=candidate_content,
            max_entries=max_entries,
        )

        eval_run_request.additional_properties = d
        return eval_run_request

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
