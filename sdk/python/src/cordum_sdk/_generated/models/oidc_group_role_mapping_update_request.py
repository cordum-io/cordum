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
    from ..models.oidc_group_role_mapping_update_request_oidc_group_role_mapping import (
        OIDCGroupRoleMappingUpdateRequestOidcGroupRoleMapping,
    )


T = TypeVar("T", bound="OIDCGroupRoleMappingUpdateRequest")


@_attrs_define
class OIDCGroupRoleMappingUpdateRequest:
    """
    Attributes:
        oidc_group_role_mapping (OIDCGroupRoleMappingUpdateRequestOidcGroupRoleMapping): JSON object mapping Okta/OIDC
            group names to Cordum roles.
        oidc_groups_claim (Union[Unset, str]): OIDC claim containing IdP group names. Default: 'groups'.
    """

    oidc_group_role_mapping: "OIDCGroupRoleMappingUpdateRequestOidcGroupRoleMapping"
    oidc_groups_claim: Union[Unset, str] = "groups"
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.oidc_group_role_mapping_update_request_oidc_group_role_mapping import (
            OIDCGroupRoleMappingUpdateRequestOidcGroupRoleMapping,
        )

        oidc_group_role_mapping = self.oidc_group_role_mapping.to_dict()

        oidc_groups_claim = self.oidc_groups_claim

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "oidc_group_role_mapping": oidc_group_role_mapping,
            }
        )
        if oidc_groups_claim is not UNSET:
            field_dict["oidc_groups_claim"] = oidc_groups_claim

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.oidc_group_role_mapping_update_request_oidc_group_role_mapping import (
            OIDCGroupRoleMappingUpdateRequestOidcGroupRoleMapping,
        )

        d = src_dict.copy()
        oidc_group_role_mapping = OIDCGroupRoleMappingUpdateRequestOidcGroupRoleMapping.from_dict(
            d.pop("oidc_group_role_mapping")
        )

        oidc_groups_claim = d.pop("oidc_groups_claim", UNSET)

        oidc_group_role_mapping_update_request = cls(
            oidc_group_role_mapping=oidc_group_role_mapping,
            oidc_groups_claim=oidc_groups_claim,
        )

        oidc_group_role_mapping_update_request.additional_properties = d
        return oidc_group_role_mapping_update_request

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
