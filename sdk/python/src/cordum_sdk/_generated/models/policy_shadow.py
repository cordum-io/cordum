from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import Dict
from typing import Union
import datetime

if TYPE_CHECKING:
    from ..models.policy_shadow_metadata import PolicyShadowMetadata


T = TypeVar("T", bound="PolicyShadow")


@_attrs_define
class PolicyShadow:
    """Full stored representation of a shadow candidate policy. `content`
    is the raw YAML source — consumers that only need to list shadows
    may prefer the server-side summary projection (not exposed as a
    separate endpoint today).

        Attributes:
            shadow_bundle_id (str): Stable `shadow-<12 hex>` handle that persists across reactivation.
            bundle_id (str): Active bundle this shadow is tied to (one shadow per bundle).
            tenant_id (str):
            content (str): Raw YAML source of the candidate policy.
            created_at (datetime.datetime):
            activated_at (datetime.datetime): Bumped on each re-activation; equals `created_at` on the first.
            created_by (str): Principal ID that activated the shadow.
            metadata (Union[Unset, PolicyShadowMetadata]): Arbitrary operator-supplied key/value pairs (e.g. ticket refs).
    """

    shadow_bundle_id: str
    bundle_id: str
    tenant_id: str
    content: str
    created_at: datetime.datetime
    activated_at: datetime.datetime
    created_by: str
    metadata: Union[Unset, "PolicyShadowMetadata"] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.policy_shadow_metadata import PolicyShadowMetadata

        shadow_bundle_id = self.shadow_bundle_id

        bundle_id = self.bundle_id

        tenant_id = self.tenant_id

        content = self.content

        created_at = self.created_at.isoformat()

        activated_at = self.activated_at.isoformat()

        created_by = self.created_by

        metadata: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.metadata, Unset):
            metadata = self.metadata.to_dict()

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "shadow_bundle_id": shadow_bundle_id,
                "bundle_id": bundle_id,
                "tenant_id": tenant_id,
                "content": content,
                "created_at": created_at,
                "activated_at": activated_at,
                "created_by": created_by,
            }
        )
        if metadata is not UNSET:
            field_dict["metadata"] = metadata

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.policy_shadow_metadata import PolicyShadowMetadata

        d = src_dict.copy()
        shadow_bundle_id = d.pop("shadow_bundle_id")

        bundle_id = d.pop("bundle_id")

        tenant_id = d.pop("tenant_id")

        content = d.pop("content")

        created_at = isoparse(d.pop("created_at"))

        activated_at = isoparse(d.pop("activated_at"))

        created_by = d.pop("created_by")

        _metadata = d.pop("metadata", UNSET)
        metadata: Union[Unset, PolicyShadowMetadata]
        if isinstance(_metadata, Unset):
            metadata = UNSET
        else:
            metadata = PolicyShadowMetadata.from_dict(_metadata)

        policy_shadow = cls(
            shadow_bundle_id=shadow_bundle_id,
            bundle_id=bundle_id,
            tenant_id=tenant_id,
            content=content,
            created_at=created_at,
            activated_at=activated_at,
            created_by=created_by,
            metadata=metadata,
        )

        policy_shadow.additional_properties = d
        return policy_shadow

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
