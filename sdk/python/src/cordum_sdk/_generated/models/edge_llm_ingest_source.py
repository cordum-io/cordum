from typing import Any, Dict, Type, TypeVar


from attrs import define as _attrs_define


T = TypeVar("T", bound="EdgeLLMIngestSource")


@_attrs_define
class EdgeLLMIngestSource:
    """
    Attributes:
        source_id (str): Stable identifier of the trusted LLM proxy; must match the authenticated proxy principal.
    """

    source_id: str

    def to_dict(self) -> Dict[str, Any]:
        source_id = self.source_id

        field_dict: Dict[str, Any] = {}
        field_dict.update(
            {
                "source_id": source_id,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        d = src_dict.copy()
        source_id = d.pop("source_id")

        edge_llm_ingest_source = cls(
            source_id=source_id,
        )

        return edge_llm_ingest_source
