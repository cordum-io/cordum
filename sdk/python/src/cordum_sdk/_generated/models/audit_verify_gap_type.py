from enum import Enum


class AuditVerifyGapType(str, Enum):
    HASH_MISMATCH = "hash_mismatch"
    MISSING = "missing"
    OUT_OF_ORDER = "out_of_order"
    RETENTION_TRIMMED = "retention_trimmed"

    def __str__(self) -> str:
        return str(self.value)
