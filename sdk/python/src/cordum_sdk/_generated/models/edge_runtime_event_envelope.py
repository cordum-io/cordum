from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.edge_runtime_event_envelope_kind import EdgeRuntimeEventEnvelopeKind
from ..models.edge_runtime_event_envelope_outcome_status import (
    EdgeRuntimeEventEnvelopeOutcomeStatus,
)
from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import cast, List
from typing import Dict
from typing import Union
import datetime

if TYPE_CHECKING:
    from ..models.edge_runtime_network_summary import EdgeRuntimeNetworkSummary
    from ..models.edge_runtime_dns_summary import EdgeRuntimeDNSSummary
    from ..models.edge_runtime_event_envelope_labels import EdgeRuntimeEventEnvelopeLabels
    from ..models.edge_artifact_pointer import EdgeArtifactPointer
    from ..models.edge_runtime_process_summary import EdgeRuntimeProcessSummary
    from ..models.edge_runtime_file_summary import EdgeRuntimeFileSummary


T = TypeVar("T", bound="EdgeRuntimeEventEnvelope")


@_attrs_define
class EdgeRuntimeEventEnvelope:
    """One bounded, redacted runtime telemetry record. Forbidden top-level keys (argv, args, cmdline, command_line, env,
    environment, file_content, file_contents, packet, payload, body, request_body, response_body, headers, header,
    cookie, cookies, secret, secrets, token, tokens, password, passwords, api_key, apikey, private_key, dns_response,
    response) are rejected at the strict-schema decode boundary.

        Attributes:
            tenant_id (str):
            session_id (str):
            execution_id (str):
            source_event_id (str):
            observed_at (datetime.datetime):
            kind (EdgeRuntimeEventEnvelopeKind):
            outcome_status (Union[Unset, EdgeRuntimeEventEnvelopeOutcomeStatus]):
            process (Union[Unset, EdgeRuntimeProcessSummary]): Bounded redacted summary of a process exec event. Raw argv /
                env / cmdline are forbidden.
            file (Union[Unset, EdgeRuntimeFileSummary]):
            network (Union[Unset, EdgeRuntimeNetworkSummary]):
            dns (Union[Unset, EdgeRuntimeDNSSummary]):
            labels (Union[Unset, EdgeRuntimeEventEnvelopeLabels]):
            artifact_ptrs (Union[Unset, List['EdgeArtifactPointer']]):
    """

    tenant_id: str
    session_id: str
    execution_id: str
    source_event_id: str
    observed_at: datetime.datetime
    kind: EdgeRuntimeEventEnvelopeKind
    outcome_status: Union[Unset, EdgeRuntimeEventEnvelopeOutcomeStatus] = UNSET
    process: Union[Unset, "EdgeRuntimeProcessSummary"] = UNSET
    file: Union[Unset, "EdgeRuntimeFileSummary"] = UNSET
    network: Union[Unset, "EdgeRuntimeNetworkSummary"] = UNSET
    dns: Union[Unset, "EdgeRuntimeDNSSummary"] = UNSET
    labels: Union[Unset, "EdgeRuntimeEventEnvelopeLabels"] = UNSET
    artifact_ptrs: Union[Unset, List["EdgeArtifactPointer"]] = UNSET

    def to_dict(self) -> Dict[str, Any]:
        from ..models.edge_runtime_network_summary import EdgeRuntimeNetworkSummary
        from ..models.edge_runtime_dns_summary import EdgeRuntimeDNSSummary
        from ..models.edge_runtime_event_envelope_labels import EdgeRuntimeEventEnvelopeLabels
        from ..models.edge_artifact_pointer import EdgeArtifactPointer
        from ..models.edge_runtime_process_summary import EdgeRuntimeProcessSummary
        from ..models.edge_runtime_file_summary import EdgeRuntimeFileSummary

        tenant_id = self.tenant_id

        session_id = self.session_id

        execution_id = self.execution_id

        source_event_id = self.source_event_id

        observed_at = self.observed_at.isoformat()

        kind = self.kind.value

        outcome_status: Union[Unset, str] = UNSET
        if not isinstance(self.outcome_status, Unset):
            outcome_status = self.outcome_status.value

        process: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.process, Unset):
            process = self.process.to_dict()

        file: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.file, Unset):
            file = self.file.to_dict()

        network: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.network, Unset):
            network = self.network.to_dict()

        dns: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.dns, Unset):
            dns = self.dns.to_dict()

        labels: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.labels, Unset):
            labels = self.labels.to_dict()

        artifact_ptrs: Union[Unset, List[Dict[str, Any]]] = UNSET
        if not isinstance(self.artifact_ptrs, Unset):
            artifact_ptrs = []
            for artifact_ptrs_item_data in self.artifact_ptrs:
                artifact_ptrs_item = artifact_ptrs_item_data.to_dict()
                artifact_ptrs.append(artifact_ptrs_item)

        field_dict: Dict[str, Any] = {}
        field_dict.update(
            {
                "tenant_id": tenant_id,
                "session_id": session_id,
                "execution_id": execution_id,
                "source_event_id": source_event_id,
                "observed_at": observed_at,
                "kind": kind,
            }
        )
        if outcome_status is not UNSET:
            field_dict["outcome_status"] = outcome_status
        if process is not UNSET:
            field_dict["process"] = process
        if file is not UNSET:
            field_dict["file"] = file
        if network is not UNSET:
            field_dict["network"] = network
        if dns is not UNSET:
            field_dict["dns"] = dns
        if labels is not UNSET:
            field_dict["labels"] = labels
        if artifact_ptrs is not UNSET:
            field_dict["artifact_ptrs"] = artifact_ptrs

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.edge_runtime_network_summary import EdgeRuntimeNetworkSummary
        from ..models.edge_runtime_dns_summary import EdgeRuntimeDNSSummary
        from ..models.edge_runtime_event_envelope_labels import EdgeRuntimeEventEnvelopeLabels
        from ..models.edge_artifact_pointer import EdgeArtifactPointer
        from ..models.edge_runtime_process_summary import EdgeRuntimeProcessSummary
        from ..models.edge_runtime_file_summary import EdgeRuntimeFileSummary

        d = src_dict.copy()
        tenant_id = d.pop("tenant_id")

        session_id = d.pop("session_id")

        execution_id = d.pop("execution_id")

        source_event_id = d.pop("source_event_id")

        observed_at = isoparse(d.pop("observed_at"))

        kind = EdgeRuntimeEventEnvelopeKind(d.pop("kind"))

        _outcome_status = d.pop("outcome_status", UNSET)
        outcome_status: Union[Unset, EdgeRuntimeEventEnvelopeOutcomeStatus]
        if isinstance(_outcome_status, Unset):
            outcome_status = UNSET
        else:
            outcome_status = EdgeRuntimeEventEnvelopeOutcomeStatus(_outcome_status)

        _process = d.pop("process", UNSET)
        process: Union[Unset, EdgeRuntimeProcessSummary]
        if isinstance(_process, Unset):
            process = UNSET
        else:
            process = EdgeRuntimeProcessSummary.from_dict(_process)

        _file = d.pop("file", UNSET)
        file: Union[Unset, EdgeRuntimeFileSummary]
        if isinstance(_file, Unset):
            file = UNSET
        else:
            file = EdgeRuntimeFileSummary.from_dict(_file)

        _network = d.pop("network", UNSET)
        network: Union[Unset, EdgeRuntimeNetworkSummary]
        if isinstance(_network, Unset):
            network = UNSET
        else:
            network = EdgeRuntimeNetworkSummary.from_dict(_network)

        _dns = d.pop("dns", UNSET)
        dns: Union[Unset, EdgeRuntimeDNSSummary]
        if isinstance(_dns, Unset):
            dns = UNSET
        else:
            dns = EdgeRuntimeDNSSummary.from_dict(_dns)

        _labels = d.pop("labels", UNSET)
        labels: Union[Unset, EdgeRuntimeEventEnvelopeLabels]
        if isinstance(_labels, Unset):
            labels = UNSET
        else:
            labels = EdgeRuntimeEventEnvelopeLabels.from_dict(_labels)

        artifact_ptrs = []
        _artifact_ptrs = d.pop("artifact_ptrs", UNSET)
        for artifact_ptrs_item_data in _artifact_ptrs or []:
            artifact_ptrs_item = EdgeArtifactPointer.from_dict(artifact_ptrs_item_data)

            artifact_ptrs.append(artifact_ptrs_item)

        edge_runtime_event_envelope = cls(
            tenant_id=tenant_id,
            session_id=session_id,
            execution_id=execution_id,
            source_event_id=source_event_id,
            observed_at=observed_at,
            kind=kind,
            outcome_status=outcome_status,
            process=process,
            file=file,
            network=network,
            dns=dns,
            labels=labels,
            artifact_ptrs=artifact_ptrs,
        )

        return edge_runtime_event_envelope
