from enum import Enum

class EdgeLLMEventEnvelopeDirection(str, Enum):
    PROMPT = "prompt"
    RESPONSE = "response"
    VALUE_0 = ""

    def __str__(self) -> str:
        return str(self.value)
