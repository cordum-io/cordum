from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..types import UNSET, Unset
from typing import cast
from typing import cast, List
from typing import Dict
from typing import Union

if TYPE_CHECKING:
    from ..models.auth_config_oidc_group_role_mapping import AuthConfigOidcGroupRoleMapping


T = TypeVar("T", bound="AuthConfig")


@_attrs_define
class AuthConfig:
    """
    Attributes:
        password_enabled (Union[Unset, bool]):
        user_auth_enabled (Union[Unset, bool]):
        saml_enabled (Union[Unset, bool]):
        saml_enterprise (Union[Unset, bool]):
        saml_login_url (Union[Unset, str]):
        saml_metadata_url (Union[Unset, str]):
        oidc_enabled (Union[Unset, bool]):
        oidc_issuer (Union[Unset, str]):
        oidc_login_url (Union[Unset, str]):
        oidc_client_id (Union[Unset, str]):
        oidc_redirect_uri (Union[Unset, str]):
        oidc_scopes (Union[Unset, List[str]]):
        oidc_groups_claim (Union[Unset, str]): OIDC claim containing IdP group names, defaulting to groups.
        oidc_group_role_mapping (Union[Unset, AuthConfigOidcGroupRoleMapping]): Case-insensitive group name to Cordum
            role mapping.
        oidc_client_secret_masked (Union[Unset, str]):
        session_ttl (Union[Unset, str]):
        require_rbac (Union[Unset, bool]):
        require_principal (Union[Unset, bool]):
        default_tenant (Union[Unset, str]):
    """

    password_enabled: Union[Unset, bool] = UNSET
    user_auth_enabled: Union[Unset, bool] = UNSET
    saml_enabled: Union[Unset, bool] = UNSET
    saml_enterprise: Union[Unset, bool] = UNSET
    saml_login_url: Union[Unset, str] = UNSET
    saml_metadata_url: Union[Unset, str] = UNSET
    oidc_enabled: Union[Unset, bool] = UNSET
    oidc_issuer: Union[Unset, str] = UNSET
    oidc_login_url: Union[Unset, str] = UNSET
    oidc_client_id: Union[Unset, str] = UNSET
    oidc_redirect_uri: Union[Unset, str] = UNSET
    oidc_scopes: Union[Unset, List[str]] = UNSET
    oidc_groups_claim: Union[Unset, str] = UNSET
    oidc_group_role_mapping: Union[Unset, "AuthConfigOidcGroupRoleMapping"] = UNSET
    oidc_client_secret_masked: Union[Unset, str] = UNSET
    session_ttl: Union[Unset, str] = UNSET
    require_rbac: Union[Unset, bool] = UNSET
    require_principal: Union[Unset, bool] = UNSET
    default_tenant: Union[Unset, str] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.auth_config_oidc_group_role_mapping import AuthConfigOidcGroupRoleMapping

        password_enabled = self.password_enabled

        user_auth_enabled = self.user_auth_enabled

        saml_enabled = self.saml_enabled

        saml_enterprise = self.saml_enterprise

        saml_login_url = self.saml_login_url

        saml_metadata_url = self.saml_metadata_url

        oidc_enabled = self.oidc_enabled

        oidc_issuer = self.oidc_issuer

        oidc_login_url = self.oidc_login_url

        oidc_client_id = self.oidc_client_id

        oidc_redirect_uri = self.oidc_redirect_uri

        oidc_scopes: Union[Unset, List[str]] = UNSET
        if not isinstance(self.oidc_scopes, Unset):
            oidc_scopes = self.oidc_scopes

        oidc_groups_claim = self.oidc_groups_claim

        oidc_group_role_mapping: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.oidc_group_role_mapping, Unset):
            oidc_group_role_mapping = self.oidc_group_role_mapping.to_dict()

        oidc_client_secret_masked = self.oidc_client_secret_masked

        session_ttl = self.session_ttl

        require_rbac = self.require_rbac

        require_principal = self.require_principal

        default_tenant = self.default_tenant

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if password_enabled is not UNSET:
            field_dict["password_enabled"] = password_enabled
        if user_auth_enabled is not UNSET:
            field_dict["user_auth_enabled"] = user_auth_enabled
        if saml_enabled is not UNSET:
            field_dict["saml_enabled"] = saml_enabled
        if saml_enterprise is not UNSET:
            field_dict["saml_enterprise"] = saml_enterprise
        if saml_login_url is not UNSET:
            field_dict["saml_login_url"] = saml_login_url
        if saml_metadata_url is not UNSET:
            field_dict["saml_metadata_url"] = saml_metadata_url
        if oidc_enabled is not UNSET:
            field_dict["oidc_enabled"] = oidc_enabled
        if oidc_issuer is not UNSET:
            field_dict["oidc_issuer"] = oidc_issuer
        if oidc_login_url is not UNSET:
            field_dict["oidc_login_url"] = oidc_login_url
        if oidc_client_id is not UNSET:
            field_dict["oidc_client_id"] = oidc_client_id
        if oidc_redirect_uri is not UNSET:
            field_dict["oidc_redirect_uri"] = oidc_redirect_uri
        if oidc_scopes is not UNSET:
            field_dict["oidc_scopes"] = oidc_scopes
        if oidc_groups_claim is not UNSET:
            field_dict["oidc_groups_claim"] = oidc_groups_claim
        if oidc_group_role_mapping is not UNSET:
            field_dict["oidc_group_role_mapping"] = oidc_group_role_mapping
        if oidc_client_secret_masked is not UNSET:
            field_dict["oidc_client_secret_masked"] = oidc_client_secret_masked
        if session_ttl is not UNSET:
            field_dict["session_ttl"] = session_ttl
        if require_rbac is not UNSET:
            field_dict["require_rbac"] = require_rbac
        if require_principal is not UNSET:
            field_dict["require_principal"] = require_principal
        if default_tenant is not UNSET:
            field_dict["default_tenant"] = default_tenant

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.auth_config_oidc_group_role_mapping import AuthConfigOidcGroupRoleMapping

        d = src_dict.copy()
        password_enabled = d.pop("password_enabled", UNSET)

        user_auth_enabled = d.pop("user_auth_enabled", UNSET)

        saml_enabled = d.pop("saml_enabled", UNSET)

        saml_enterprise = d.pop("saml_enterprise", UNSET)

        saml_login_url = d.pop("saml_login_url", UNSET)

        saml_metadata_url = d.pop("saml_metadata_url", UNSET)

        oidc_enabled = d.pop("oidc_enabled", UNSET)

        oidc_issuer = d.pop("oidc_issuer", UNSET)

        oidc_login_url = d.pop("oidc_login_url", UNSET)

        oidc_client_id = d.pop("oidc_client_id", UNSET)

        oidc_redirect_uri = d.pop("oidc_redirect_uri", UNSET)

        oidc_scopes = cast(List[str], d.pop("oidc_scopes", UNSET))

        oidc_groups_claim = d.pop("oidc_groups_claim", UNSET)

        _oidc_group_role_mapping = d.pop("oidc_group_role_mapping", UNSET)
        oidc_group_role_mapping: Union[Unset, AuthConfigOidcGroupRoleMapping]
        if isinstance(_oidc_group_role_mapping, Unset):
            oidc_group_role_mapping = UNSET
        else:
            oidc_group_role_mapping = AuthConfigOidcGroupRoleMapping.from_dict(
                _oidc_group_role_mapping
            )

        oidc_client_secret_masked = d.pop("oidc_client_secret_masked", UNSET)

        session_ttl = d.pop("session_ttl", UNSET)

        require_rbac = d.pop("require_rbac", UNSET)

        require_principal = d.pop("require_principal", UNSET)

        default_tenant = d.pop("default_tenant", UNSET)

        auth_config = cls(
            password_enabled=password_enabled,
            user_auth_enabled=user_auth_enabled,
            saml_enabled=saml_enabled,
            saml_enterprise=saml_enterprise,
            saml_login_url=saml_login_url,
            saml_metadata_url=saml_metadata_url,
            oidc_enabled=oidc_enabled,
            oidc_issuer=oidc_issuer,
            oidc_login_url=oidc_login_url,
            oidc_client_id=oidc_client_id,
            oidc_redirect_uri=oidc_redirect_uri,
            oidc_scopes=oidc_scopes,
            oidc_groups_claim=oidc_groups_claim,
            oidc_group_role_mapping=oidc_group_role_mapping,
            oidc_client_secret_masked=oidc_client_secret_masked,
            session_ttl=session_ttl,
            require_rbac=require_rbac,
            require_principal=require_principal,
            default_tenant=default_tenant,
        )

        auth_config.additional_properties = d
        return auth_config

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
