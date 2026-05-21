from enum import Enum


class SetTelemetryConsentBodyMode(str, Enum):
    ANONYMOUS = "anonymous"
    LOCAL_ONLY = "local_only"
    OFF = "off"

    def __str__(self) -> str:
        return str(self.value)
