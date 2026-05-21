from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.oidc_group_role_mapping_update_request_oidc_group_role_mapping_additional_property import (
    OIDCGroupRoleMappingUpdateRequestOidcGroupRoleMappingAdditionalProperty,
)


T = TypeVar("T", bound="OIDCGroupRoleMappingUpdateRequestOidcGroupRoleMapping")


@_attrs_define
class OIDCGroupRoleMappingUpdateRequestOidcGroupRoleMapping:
    """JSON object mapping Okta/OIDC group names to Cordum roles."""

    additional_properties: Dict[
        str, OIDCGroupRoleMappingUpdateRequestOidcGroupRoleMappingAdditionalProperty
    ] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        field_dict: Dict[str, Any] = {}
        for prop_name, prop in self.additional_properties.items():
            field_dict[prop_name] = prop.value

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        oidc_group_role_mapping_update_request_oidc_group_role_mapping = cls()

        additional_properties = {}
        for prop_name, prop_dict in d.items():
            additional_property = (
                OIDCGroupRoleMappingUpdateRequestOidcGroupRoleMappingAdditionalProperty(prop_dict)
            )

            additional_properties[prop_name] = additional_property

        oidc_group_role_mapping_update_request_oidc_group_role_mapping.additional_properties = (
            additional_properties
        )
        return oidc_group_role_mapping_update_request_oidc_group_role_mapping

    @property
    def additional_keys(self) -> List[str]:
        return list(self.additional_properties.keys())

    def __getitem__(
        self, key: str
    ) -> OIDCGroupRoleMappingUpdateRequestOidcGroupRoleMappingAdditionalProperty:
        return self.additional_properties[key]

    def __setitem__(
        self,
        key: str,
        value: OIDCGroupRoleMappingUpdateRequestOidcGroupRoleMappingAdditionalProperty,
    ) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
