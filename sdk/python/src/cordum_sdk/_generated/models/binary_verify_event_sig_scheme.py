from enum import Enum


class BinaryVerifyEventSigScheme(str, Enum):
    AUTHENTICODE = "authenticode"
    CODESIGN = "codesign"
    DEV = "dev"
    GPG = "gpg"

    def __str__(self) -> str:
        return str(self.value)
