from enum import Enum


class AuthConfigOidcGroupRoleMappingAdditionalProperty(str, Enum):
    ADMIN = "admin"
    OPERATOR = "operator"
    VIEWER = "viewer"

    def __str__(self) -> str:
        return str(self.value)
