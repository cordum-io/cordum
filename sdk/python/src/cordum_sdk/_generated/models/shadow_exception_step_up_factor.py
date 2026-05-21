from enum import Enum


class ShadowExceptionStepUpFactor(str, Enum):
    MFA_RECENT = "mfa_recent"
    NONE = "none"
    SIGNED_ADMIN_TOKEN = "signed_admin_token"

    def __str__(self) -> str:
        return str(self.value)
