from enum import Enum


class WorkerCredentialProofAlgorithm(str, Enum):
    ECDSA_P256_SHA256 = "ECDSA_P256_SHA256"

    def __str__(self) -> str:
        return str(self.value)
