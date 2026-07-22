from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.edge_llm_event_decision_decision import EdgeLLMEventDecisionDecision
from ..types import UNSET, Unset
from typing import cast
from typing import cast, List
from typing import Dict
from typing import Union

if TYPE_CHECKING:
  from ..models.edge_llm_message import EdgeLLMMessage





T = TypeVar("T", bound="EdgeLLMEventDecision")


@_attrs_define
class EdgeLLMEventDecision:
    """ Per-event advisory outcome. `decision=redact` means a secret was detected and the proxy should forward
    `redacted_content` (subject to `truncated`) instead of the original. This is NOT a policy allow/deny decision.

        Attributes:
            source_event_id (str):
            kind (str):
            decision (EdgeLLMEventDecisionDecision):
            redacted (bool):
            redaction_complete (bool): Whether this decision reflects a scan of the FULL turn content.
                Always true except for kind=llm.stream.chunk, where it is true
                ONLY when the chunk was submitted with final=true (which requires
                the full aggregated content). A non-final chunk is scanned in
                isolation and can miss a secret split across a chunk boundary —
                proxies MUST NOT treat redaction_complete=false as a governance
                verdict for forwarding purposes.
            truncated (Union[Unset, bool]):
            redacted_content (Union[Unset, str]):
            redacted_messages (Union[Unset, List['EdgeLLMMessage']]): Role-preserving redacted chat messages when the
                submitted event used `messages`. Absent for content-only (single-string) envelopes.
            findings (Union[Unset, List[str]]): Detected secret finding TYPES (never values), e.g. aws_credential,
                bearer_token, private_key.
     """

    source_event_id: str
    kind: str
    decision: EdgeLLMEventDecisionDecision
    redacted: bool
    redaction_complete: bool
    truncated: Union[Unset, bool] = UNSET
    redacted_content: Union[Unset, str] = UNSET
    redacted_messages: Union[Unset, List['EdgeLLMMessage']] = UNSET
    findings: Union[Unset, List[str]] = UNSET


    def to_dict(self) -> Dict[str, Any]:
        from ..models.edge_llm_message import EdgeLLMMessage
        source_event_id = self.source_event_id

        kind = self.kind

        decision = self.decision.value

        redacted = self.redacted

        redaction_complete = self.redaction_complete

        truncated = self.truncated

        redacted_content = self.redacted_content

        redacted_messages: Union[Unset, List[Dict[str, Any]]] = UNSET
        if not isinstance(self.redacted_messages, Unset):
            redacted_messages = []
            for redacted_messages_item_data in self.redacted_messages:
                redacted_messages_item = redacted_messages_item_data.to_dict()
                redacted_messages.append(redacted_messages_item)



        findings: Union[Unset, List[str]] = UNSET
        if not isinstance(self.findings, Unset):
            findings = self.findings




        field_dict: Dict[str, Any] = {}
        field_dict.update({
            "source_event_id": source_event_id,
            "kind": kind,
            "decision": decision,
            "redacted": redacted,
            "redaction_complete": redaction_complete,
        })
        if truncated is not UNSET:
            field_dict["truncated"] = truncated
        if redacted_content is not UNSET:
            field_dict["redacted_content"] = redacted_content
        if redacted_messages is not UNSET:
            field_dict["redacted_messages"] = redacted_messages
        if findings is not UNSET:
            field_dict["findings"] = findings

        return field_dict



    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.edge_llm_message import EdgeLLMMessage
        d = src_dict.copy()
        source_event_id = d.pop("source_event_id")

        kind = d.pop("kind")

        decision = EdgeLLMEventDecisionDecision(d.pop("decision"))




        redacted = d.pop("redacted")

        redaction_complete = d.pop("redaction_complete")

        truncated = d.pop("truncated", UNSET)

        redacted_content = d.pop("redacted_content", UNSET)

        redacted_messages = []
        _redacted_messages = d.pop("redacted_messages", UNSET)
        for redacted_messages_item_data in (_redacted_messages or []):
            redacted_messages_item = EdgeLLMMessage.from_dict(redacted_messages_item_data)



            redacted_messages.append(redacted_messages_item)


        findings = cast(List[str], d.pop("findings", UNSET))


        edge_llm_event_decision = cls(
            source_event_id=source_event_id,
            kind=kind,
            decision=decision,
            redacted=redacted,
            redaction_complete=redaction_complete,
            truncated=truncated,
            redacted_content=redacted_content,
            redacted_messages=redacted_messages,
            findings=findings,
        )

        return edge_llm_event_decision

