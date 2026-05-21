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
    from ..models.policy_shadow_upsert_request_metadata import PolicyShadowUpsertRequestMetadata


T = TypeVar("T", bound="PolicyShadowUpsertRequest")


@_attrs_define
class PolicyShadowUpsertRequest:
    """PUT body for activating or replacing a shadow policy.

    Attributes:
        content (str): Raw YAML source of the candidate policy. Required.
        metadata (Union[Unset, PolicyShadowUpsertRequestMetadata]): Arbitrary operator-supplied key/value pairs;
            optional.
    """

    content: str
    metadata: Union[Unset, "PolicyShadowUpsertRequestMetadata"] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.policy_shadow_upsert_request_metadata import PolicyShadowUpsertRequestMetadata

        content = self.content

        metadata: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.metadata, Unset):
            metadata = self.metadata.to_dict()

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "content": content,
            }
        )
        if metadata is not UNSET:
            field_dict["metadata"] = metadata

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.policy_shadow_upsert_request_metadata import PolicyShadowUpsertRequestMetadata

        d = src_dict.copy()
        content = d.pop("content")

        _metadata = d.pop("metadata", UNSET)
        metadata: Union[Unset, PolicyShadowUpsertRequestMetadata]
        if isinstance(_metadata, Unset):
            metadata = UNSET
        else:
            metadata = PolicyShadowUpsertRequestMetadata.from_dict(_metadata)

        policy_shadow_upsert_request = cls(
            content=content,
            metadata=metadata,
        )

        policy_shadow_upsert_request.additional_properties = d
        return policy_shadow_upsert_request

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
