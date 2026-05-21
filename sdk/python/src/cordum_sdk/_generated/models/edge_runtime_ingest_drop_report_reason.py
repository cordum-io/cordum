from enum import Enum


class EdgeRuntimeIngestDropReportReason(str, Enum):
    SAMPLED_OUT = "sampled_out"

    def __str__(self) -> str:
        return str(self.value)
