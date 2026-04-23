from enum import Enum

class ExportAuditComplianceFormat(str, Enum):
    CSV = "csv"
    NDJSON = "ndjson"

    def __str__(self) -> str:
        return str(self.value)
