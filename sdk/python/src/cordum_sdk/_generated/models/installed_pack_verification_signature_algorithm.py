from enum import Enum


class InstalledPackVerificationSignatureAlgorithm(str, Enum):
    ED25519 = "ed25519"

    def __str__(self) -> str:
        return str(self.value)
