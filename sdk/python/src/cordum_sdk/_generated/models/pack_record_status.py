from enum import Enum


class PackRecordStatus(str, Enum):
    ACTIVE = "ACTIVE"
    ERROR = "ERROR"
    INACTIVE = "INACTIVE"
    UNINSTALLED = "UNINSTALLED"

    def __str__(self) -> str:
        return str(self.value)
