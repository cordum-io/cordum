from enum import Enum

class EdgeLLMEventEnvelopeKind(str, Enum):
    LLM_COST_RECORDED = "llm.cost.recorded"
    LLM_REQUEST_POST = "llm.request.post"
    LLM_REQUEST_PRE = "llm.request.pre"
    LLM_STREAM_CHUNK = "llm.stream.chunk"

    def __str__(self) -> str:
        return str(self.value)
