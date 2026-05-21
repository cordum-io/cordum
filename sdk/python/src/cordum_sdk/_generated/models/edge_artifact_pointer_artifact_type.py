from enum import Enum


class EdgeArtifactPointerArtifactType(str, Enum):
    EDGE_DIFF = "edge.diff"
    EDGE_EVIDENCE_BUNDLE = "edge.evidence_bundle"
    EDGE_TOOL_INPUT = "edge.tool_input"
    EDGE_TOOL_RESULT = "edge.tool_result"
    EDGE_TRANSCRIPT = "edge.transcript"

    def __str__(self) -> str:
        return str(self.value)
