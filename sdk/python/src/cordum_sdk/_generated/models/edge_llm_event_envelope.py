from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.edge_llm_event_envelope_direction import EdgeLLMEventEnvelopeDirection
from ..models.edge_llm_event_envelope_kind import EdgeLLMEventEnvelopeKind
from ..models.edge_llm_event_envelope_outcome_status import EdgeLLMEventEnvelopeOutcomeStatus
from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import cast, List
from typing import Dict
from typing import Union
import datetime

if TYPE_CHECKING:
  from ..models.edge_llm_message import EdgeLLMMessage
  from ..models.edge_llm_tokens import EdgeLLMTokens
  from ..models.edge_llm_event_envelope_labels import EdgeLLMEventEnvelopeLabels
  from ..models.edge_artifact_pointer import EdgeArtifactPointer





T = TypeVar("T", bound="EdgeLLMEventEnvelope")


@_attrs_define
class EdgeLLMEventEnvelope:
    """ One intercepted LLM interaction. Smuggled keys (authorization, headers, cookies, api_key, provider keys) are
    rejected at the strict-schema decode boundary; content and messages are redacted by the gateway before persistence.

        Attributes:
            tenant_id (str):
            session_id (str):
            execution_id (str):
            source_event_id (str):
            observed_at (datetime.datetime):
            kind (EdgeLLMEventEnvelopeKind): llm.stream.chunk carries one delta of a streamed response and is
                scanned in isolation — a secret split across a chunk boundary can
                evade per-chunk redaction. A chunk is redaction-complete (see
                EdgeLLMEventDecision.redaction_complete) ONLY when submitted with
                final=true and the full aggregated content/messages for the
                stream. See docs/edge/llm-proxy-governance.md "Streaming chunk
                redaction limits".
            outcome_status (Union[Unset, EdgeLLMEventEnvelopeOutcomeStatus]):
            agent_product (Union[Unset, str]):
            provider (Union[Unset, str]):
            model (Union[Unset, str]):
            direction (Union[Unset, EdgeLLMEventEnvelopeDirection]):
            content (Union[Unset, str]): Prompt or completion text; bounded by the 1 MiB raw-envelope cap and redacted by
                the gateway before persistence.
            messages (Union[Unset, List['EdgeLLMMessage']]):
            tokens (Union[Unset, EdgeLLMTokens]): Optional token accounting for usage/cost evidence.
            cost_usd (Union[Unset, float]):
            labels (Union[Unset, EdgeLLMEventEnvelopeLabels]):
            artifact_ptrs (Union[Unset, List['EdgeArtifactPointer']]):
            stream_id (Union[Unset, str]): Groups the chunks of one streamed response. Only meaningful for
                kind=llm.stream.chunk; reserved as the key for a future server-side reassembly pass.
            sequence (Union[Unset, int]): 0-based chunk position within stream_id. Only meaningful for
                kind=llm.stream.chunk.
            final (Union[Unset, bool]): Marks the last chunk of a stream. When true, content (or messages) MUST carry the
                full aggregated response text, not just the last delta — required for the mandatory redaction scan to be
                complete. A final=true chunk with no content or messages is rejected.
     """

    tenant_id: str
    session_id: str
    execution_id: str
    source_event_id: str
    observed_at: datetime.datetime
    kind: EdgeLLMEventEnvelopeKind
    outcome_status: Union[Unset, EdgeLLMEventEnvelopeOutcomeStatus] = UNSET
    agent_product: Union[Unset, str] = UNSET
    provider: Union[Unset, str] = UNSET
    model: Union[Unset, str] = UNSET
    direction: Union[Unset, EdgeLLMEventEnvelopeDirection] = UNSET
    content: Union[Unset, str] = UNSET
    messages: Union[Unset, List['EdgeLLMMessage']] = UNSET
    tokens: Union[Unset, 'EdgeLLMTokens'] = UNSET
    cost_usd: Union[Unset, float] = UNSET
    labels: Union[Unset, 'EdgeLLMEventEnvelopeLabels'] = UNSET
    artifact_ptrs: Union[Unset, List['EdgeArtifactPointer']] = UNSET
    stream_id: Union[Unset, str] = UNSET
    sequence: Union[Unset, int] = UNSET
    final: Union[Unset, bool] = UNSET


    def to_dict(self) -> Dict[str, Any]:
        from ..models.edge_llm_message import EdgeLLMMessage
        from ..models.edge_llm_tokens import EdgeLLMTokens
        from ..models.edge_llm_event_envelope_labels import EdgeLLMEventEnvelopeLabels
        from ..models.edge_artifact_pointer import EdgeArtifactPointer
        tenant_id = self.tenant_id

        session_id = self.session_id

        execution_id = self.execution_id

        source_event_id = self.source_event_id

        observed_at = self.observed_at.isoformat()

        kind = self.kind.value

        outcome_status: Union[Unset, str] = UNSET
        if not isinstance(self.outcome_status, Unset):
            outcome_status = self.outcome_status.value


        agent_product = self.agent_product

        provider = self.provider

        model = self.model

        direction: Union[Unset, str] = UNSET
        if not isinstance(self.direction, Unset):
            direction = self.direction.value


        content = self.content

        messages: Union[Unset, List[Dict[str, Any]]] = UNSET
        if not isinstance(self.messages, Unset):
            messages = []
            for messages_item_data in self.messages:
                messages_item = messages_item_data.to_dict()
                messages.append(messages_item)



        tokens: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.tokens, Unset):
            tokens = self.tokens.to_dict()

        cost_usd = self.cost_usd

        labels: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.labels, Unset):
            labels = self.labels.to_dict()

        artifact_ptrs: Union[Unset, List[Dict[str, Any]]] = UNSET
        if not isinstance(self.artifact_ptrs, Unset):
            artifact_ptrs = []
            for artifact_ptrs_item_data in self.artifact_ptrs:
                artifact_ptrs_item = artifact_ptrs_item_data.to_dict()
                artifact_ptrs.append(artifact_ptrs_item)



        stream_id = self.stream_id

        sequence = self.sequence

        final = self.final


        field_dict: Dict[str, Any] = {}
        field_dict.update({
            "tenant_id": tenant_id,
            "session_id": session_id,
            "execution_id": execution_id,
            "source_event_id": source_event_id,
            "observed_at": observed_at,
            "kind": kind,
        })
        if outcome_status is not UNSET:
            field_dict["outcome_status"] = outcome_status
        if agent_product is not UNSET:
            field_dict["agent_product"] = agent_product
        if provider is not UNSET:
            field_dict["provider"] = provider
        if model is not UNSET:
            field_dict["model"] = model
        if direction is not UNSET:
            field_dict["direction"] = direction
        if content is not UNSET:
            field_dict["content"] = content
        if messages is not UNSET:
            field_dict["messages"] = messages
        if tokens is not UNSET:
            field_dict["tokens"] = tokens
        if cost_usd is not UNSET:
            field_dict["cost_usd"] = cost_usd
        if labels is not UNSET:
            field_dict["labels"] = labels
        if artifact_ptrs is not UNSET:
            field_dict["artifact_ptrs"] = artifact_ptrs
        if stream_id is not UNSET:
            field_dict["stream_id"] = stream_id
        if sequence is not UNSET:
            field_dict["sequence"] = sequence
        if final is not UNSET:
            field_dict["final"] = final

        return field_dict



    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.edge_llm_message import EdgeLLMMessage
        from ..models.edge_llm_tokens import EdgeLLMTokens
        from ..models.edge_llm_event_envelope_labels import EdgeLLMEventEnvelopeLabels
        from ..models.edge_artifact_pointer import EdgeArtifactPointer
        d = src_dict.copy()
        tenant_id = d.pop("tenant_id")

        session_id = d.pop("session_id")

        execution_id = d.pop("execution_id")

        source_event_id = d.pop("source_event_id")

        observed_at = isoparse(d.pop("observed_at"))




        kind = EdgeLLMEventEnvelopeKind(d.pop("kind"))




        _outcome_status = d.pop("outcome_status", UNSET)
        outcome_status: Union[Unset, EdgeLLMEventEnvelopeOutcomeStatus]
        if isinstance(_outcome_status,  Unset):
            outcome_status = UNSET
        else:
            outcome_status = EdgeLLMEventEnvelopeOutcomeStatus(_outcome_status)




        agent_product = d.pop("agent_product", UNSET)

        provider = d.pop("provider", UNSET)

        model = d.pop("model", UNSET)

        _direction = d.pop("direction", UNSET)
        direction: Union[Unset, EdgeLLMEventEnvelopeDirection]
        if isinstance(_direction,  Unset):
            direction = UNSET
        else:
            direction = EdgeLLMEventEnvelopeDirection(_direction)




        content = d.pop("content", UNSET)

        messages = []
        _messages = d.pop("messages", UNSET)
        for messages_item_data in (_messages or []):
            messages_item = EdgeLLMMessage.from_dict(messages_item_data)



            messages.append(messages_item)


        _tokens = d.pop("tokens", UNSET)
        tokens: Union[Unset, EdgeLLMTokens]
        if isinstance(_tokens,  Unset):
            tokens = UNSET
        else:
            tokens = EdgeLLMTokens.from_dict(_tokens)




        cost_usd = d.pop("cost_usd", UNSET)

        _labels = d.pop("labels", UNSET)
        labels: Union[Unset, EdgeLLMEventEnvelopeLabels]
        if isinstance(_labels,  Unset):
            labels = UNSET
        else:
            labels = EdgeLLMEventEnvelopeLabels.from_dict(_labels)




        artifact_ptrs = []
        _artifact_ptrs = d.pop("artifact_ptrs", UNSET)
        for artifact_ptrs_item_data in (_artifact_ptrs or []):
            artifact_ptrs_item = EdgeArtifactPointer.from_dict(artifact_ptrs_item_data)



            artifact_ptrs.append(artifact_ptrs_item)


        stream_id = d.pop("stream_id", UNSET)

        sequence = d.pop("sequence", UNSET)

        final = d.pop("final", UNSET)

        edge_llm_event_envelope = cls(
            tenant_id=tenant_id,
            session_id=session_id,
            execution_id=execution_id,
            source_event_id=source_event_id,
            observed_at=observed_at,
            kind=kind,
            outcome_status=outcome_status,
            agent_product=agent_product,
            provider=provider,
            model=model,
            direction=direction,
            content=content,
            messages=messages,
            tokens=tokens,
            cost_usd=cost_usd,
            labels=labels,
            artifact_ptrs=artifact_ptrs,
            stream_id=stream_id,
            sequence=sequence,
            final=final,
        )

        return edge_llm_event_envelope

