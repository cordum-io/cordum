from enum import Enum


class AuthSource(str, Enum):
    API_KEY = "api_key"
    JWT = "jwt"
    OIDC = "oidc"
    SESSION = "session"

    def __str__(self) -> str:
        return str(self.value)
