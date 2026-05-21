from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.shadow_agent_finding_ci_provider import ShadowAgentFindingCiProvider
from ..models.shadow_agent_finding_retention_class import ShadowAgentFindingRetentionClass
from ..models.shadow_agent_finding_risk import ShadowAgentFindingRisk
from ..models.shadow_agent_finding_source_type import ShadowAgentFindingSourceType
from ..models.shadow_agent_finding_status import ShadowAgentFindingStatus
from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import cast, List
from typing import cast, Union
from typing import Dict
from typing import Union
import datetime

if TYPE_CHECKING:
    from ..models.shadow_evidence_pointer import ShadowEvidencePointer
    from ..models.shadow_agent_finding_metadata import ShadowAgentFindingMetadata


T = TypeVar("T", bound="ShadowAgentFinding")


@_attrs_define
class ShadowAgentFinding:
    """Persisted lifecycle record for one detected shadow agent. Distinct from the scanner-only `Finding` shape; this is
    the operator-visible triage record consumed by /api/v1/edge/shadow-agents.

        Attributes:
            finding_id (str): Server-generated id prefixed with `edge_shadow_`.
            tenant_id (str):
            owner_principal_id (str):
            principal_id (str): Detector identity (scanner principal); falls back to authenticated caller when omitted.
            agent_product (str):
            risk (ShadowAgentFindingRisk):
            status (ShadowAgentFindingStatus):
            evidence_type (str):
            detected_at (datetime.datetime):
            created_at (datetime.datetime):
            updated_at (datetime.datetime):
            agent_id (Union[Unset, str]):
            hostname (Union[Unset, str]):
            evidence_summary (Union[Unset, str]): Bounded (≤2 KiB) redacted summary safe to persist and return. Secret-
                shaped values are stripped at ingest.
            evidence_artifact_ptr (Union[Unset, ShadowEvidencePointer]): Reference to redacted evidence stored outside the
                finding record. Distinct from `ArtifactPointer` because shadow findings have no session/execution context.
            redacted_path (Union[Unset, str]): Home-prefix-stripped path; never an absolute developer-machine path.
            resolved_at (Union[None, Unset, datetime.datetime]):
            resolved_by (Union[Unset, str]):
            resolution_reason (Union[Unset, str]): Bounded (≤512 bytes) human reason supplied at resolve/suppress; secret
                markers stripped.
            suppressed_until (Union[None, Unset, datetime.datetime]): Optional hint for a time-bound suppression. The store
                does NOT auto-revert when the timestamp lapses.
            metadata (Union[Unset, ShadowAgentFindingMetadata]): Small free-form map (≤16 entries, ≤64-byte keys, ≤256-byte
                values).
            source_type (Union[Unset, ShadowAgentFindingSourceType]): EDGE-143.5 §10.1 — detector-family identifier.
                Defaults to `local` on read for legacy EDGE-141 records.
            source_id (Union[Unset, str]): Detector instance identifier.
            cluster_id (Union[Unset, str]): Operator-configured K8s cluster name; empty for non-K8s sources.
            namespace (Union[Unset, str]): K8s namespace; empty for non-K8s sources.
            workload_kind (Union[Unset, str]): K8s kind (Deployment, StatefulSet, DaemonSet, ...) or empty.
            workload_name (Union[Unset, str]):
            pod_uid (Union[Unset, str]): Optional pod UUID when the finding pins a specific pod.
            ci_provider (Union[Unset, ShadowAgentFindingCiProvider]): EDGE-143.5 §10.1 — CI provider; empty for non-CI
                sources.
            repo (Union[Unset, str]): `org/repo` for CI sources; composite-indexed with ci_provider.
            ref (Union[Unset, str]):
            workflow_id (Union[Unset, str]):
            job_id (Union[Unset, str]):
            run_id (Union[Unset, str]):
            runner_id (Union[Unset, str]):
            tenant_source (Union[Unset, str]): Audit trail for tenant-attribution decisions (see §6.1 / §6.3).
            principal_source (Union[Unset, str]): Audit trail for principal-attribution decisions (see §6.2 / §6.4).
            signal_set (Union[Unset, List[str]]): Bounded enum-shape signal identifiers from §7.1 / §8.x. Supports any-of
                filtering via the `signal` query param.
            confidence (Union[Unset, float]): Detector self-rated confidence in [0, 1].
            first_seen (Union[None, Unset, datetime.datetime]): Distinct from `observed_at`; populated by detectors with
                longitudinal tracking.
            last_seen (Union[None, Unset, datetime.datetime]): Updated on every re-observation.
            false_positive_reason (Union[Unset, str]): Populated when `status` would be `managed_skip` per §10.3.
            exception_id (Union[Unset, str]): Joins to operator-defined exception declarations (§10.3).
            retention_class (Union[Unset, ShadowAgentFindingRetentionClass]): EDGE-143.5 §10.5 — per-finding terminal-TTL
                class. Empty falls back to the store's `terminalRetention`.
    """

    finding_id: str
    tenant_id: str
    owner_principal_id: str
    principal_id: str
    agent_product: str
    risk: ShadowAgentFindingRisk
    status: ShadowAgentFindingStatus
    evidence_type: str
    detected_at: datetime.datetime
    created_at: datetime.datetime
    updated_at: datetime.datetime
    agent_id: Union[Unset, str] = UNSET
    hostname: Union[Unset, str] = UNSET
    evidence_summary: Union[Unset, str] = UNSET
    evidence_artifact_ptr: Union[Unset, "ShadowEvidencePointer"] = UNSET
    redacted_path: Union[Unset, str] = UNSET
    resolved_at: Union[None, Unset, datetime.datetime] = UNSET
    resolved_by: Union[Unset, str] = UNSET
    resolution_reason: Union[Unset, str] = UNSET
    suppressed_until: Union[None, Unset, datetime.datetime] = UNSET
    metadata: Union[Unset, "ShadowAgentFindingMetadata"] = UNSET
    source_type: Union[Unset, ShadowAgentFindingSourceType] = UNSET
    source_id: Union[Unset, str] = UNSET
    cluster_id: Union[Unset, str] = UNSET
    namespace: Union[Unset, str] = UNSET
    workload_kind: Union[Unset, str] = UNSET
    workload_name: Union[Unset, str] = UNSET
    pod_uid: Union[Unset, str] = UNSET
    ci_provider: Union[Unset, ShadowAgentFindingCiProvider] = UNSET
    repo: Union[Unset, str] = UNSET
    ref: Union[Unset, str] = UNSET
    workflow_id: Union[Unset, str] = UNSET
    job_id: Union[Unset, str] = UNSET
    run_id: Union[Unset, str] = UNSET
    runner_id: Union[Unset, str] = UNSET
    tenant_source: Union[Unset, str] = UNSET
    principal_source: Union[Unset, str] = UNSET
    signal_set: Union[Unset, List[str]] = UNSET
    confidence: Union[Unset, float] = UNSET
    first_seen: Union[None, Unset, datetime.datetime] = UNSET
    last_seen: Union[None, Unset, datetime.datetime] = UNSET
    false_positive_reason: Union[Unset, str] = UNSET
    exception_id: Union[Unset, str] = UNSET
    retention_class: Union[Unset, ShadowAgentFindingRetentionClass] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.shadow_evidence_pointer import ShadowEvidencePointer
        from ..models.shadow_agent_finding_metadata import ShadowAgentFindingMetadata

        finding_id = self.finding_id

        tenant_id = self.tenant_id

        owner_principal_id = self.owner_principal_id

        principal_id = self.principal_id

        agent_product = self.agent_product

        risk = self.risk.value

        status = self.status.value

        evidence_type = self.evidence_type

        detected_at = self.detected_at.isoformat()

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        agent_id = self.agent_id

        hostname = self.hostname

        evidence_summary = self.evidence_summary

        evidence_artifact_ptr: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.evidence_artifact_ptr, Unset):
            evidence_artifact_ptr = self.evidence_artifact_ptr.to_dict()

        redacted_path = self.redacted_path

        resolved_at: Union[None, Unset, str]
        if isinstance(self.resolved_at, Unset):
            resolved_at = UNSET
        elif isinstance(self.resolved_at, datetime.datetime):
            resolved_at = self.resolved_at.isoformat()
        else:
            resolved_at = self.resolved_at

        resolved_by = self.resolved_by

        resolution_reason = self.resolution_reason

        suppressed_until: Union[None, Unset, str]
        if isinstance(self.suppressed_until, Unset):
            suppressed_until = UNSET
        elif isinstance(self.suppressed_until, datetime.datetime):
            suppressed_until = self.suppressed_until.isoformat()
        else:
            suppressed_until = self.suppressed_until

        metadata: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.metadata, Unset):
            metadata = self.metadata.to_dict()

        source_type: Union[Unset, str] = UNSET
        if not isinstance(self.source_type, Unset):
            source_type = self.source_type.value

        source_id = self.source_id

        cluster_id = self.cluster_id

        namespace = self.namespace

        workload_kind = self.workload_kind

        workload_name = self.workload_name

        pod_uid = self.pod_uid

        ci_provider: Union[Unset, str] = UNSET
        if not isinstance(self.ci_provider, Unset):
            ci_provider = self.ci_provider.value

        repo = self.repo

        ref = self.ref

        workflow_id = self.workflow_id

        job_id = self.job_id

        run_id = self.run_id

        runner_id = self.runner_id

        tenant_source = self.tenant_source

        principal_source = self.principal_source

        signal_set: Union[Unset, List[str]] = UNSET
        if not isinstance(self.signal_set, Unset):
            signal_set = self.signal_set

        confidence = self.confidence

        first_seen: Union[None, Unset, str]
        if isinstance(self.first_seen, Unset):
            first_seen = UNSET
        elif isinstance(self.first_seen, datetime.datetime):
            first_seen = self.first_seen.isoformat()
        else:
            first_seen = self.first_seen

        last_seen: Union[None, Unset, str]
        if isinstance(self.last_seen, Unset):
            last_seen = UNSET
        elif isinstance(self.last_seen, datetime.datetime):
            last_seen = self.last_seen.isoformat()
        else:
            last_seen = self.last_seen

        false_positive_reason = self.false_positive_reason

        exception_id = self.exception_id

        retention_class: Union[Unset, str] = UNSET
        if not isinstance(self.retention_class, Unset):
            retention_class = self.retention_class.value

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "finding_id": finding_id,
                "tenant_id": tenant_id,
                "owner_principal_id": owner_principal_id,
                "principal_id": principal_id,
                "agent_product": agent_product,
                "risk": risk,
                "status": status,
                "evidence_type": evidence_type,
                "detected_at": detected_at,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if agent_id is not UNSET:
            field_dict["agent_id"] = agent_id
        if hostname is not UNSET:
            field_dict["hostname"] = hostname
        if evidence_summary is not UNSET:
            field_dict["evidence_summary"] = evidence_summary
        if evidence_artifact_ptr is not UNSET:
            field_dict["evidence_artifact_ptr"] = evidence_artifact_ptr
        if redacted_path is not UNSET:
            field_dict["redacted_path"] = redacted_path
        if resolved_at is not UNSET:
            field_dict["resolved_at"] = resolved_at
        if resolved_by is not UNSET:
            field_dict["resolved_by"] = resolved_by
        if resolution_reason is not UNSET:
            field_dict["resolution_reason"] = resolution_reason
        if suppressed_until is not UNSET:
            field_dict["suppressed_until"] = suppressed_until
        if metadata is not UNSET:
            field_dict["metadata"] = metadata
        if source_type is not UNSET:
            field_dict["source_type"] = source_type
        if source_id is not UNSET:
            field_dict["source_id"] = source_id
        if cluster_id is not UNSET:
            field_dict["cluster_id"] = cluster_id
        if namespace is not UNSET:
            field_dict["namespace"] = namespace
        if workload_kind is not UNSET:
            field_dict["workload_kind"] = workload_kind
        if workload_name is not UNSET:
            field_dict["workload_name"] = workload_name
        if pod_uid is not UNSET:
            field_dict["pod_uid"] = pod_uid
        if ci_provider is not UNSET:
            field_dict["ci_provider"] = ci_provider
        if repo is not UNSET:
            field_dict["repo"] = repo
        if ref is not UNSET:
            field_dict["ref"] = ref
        if workflow_id is not UNSET:
            field_dict["workflow_id"] = workflow_id
        if job_id is not UNSET:
            field_dict["job_id"] = job_id
        if run_id is not UNSET:
            field_dict["run_id"] = run_id
        if runner_id is not UNSET:
            field_dict["runner_id"] = runner_id
        if tenant_source is not UNSET:
            field_dict["tenant_source"] = tenant_source
        if principal_source is not UNSET:
            field_dict["principal_source"] = principal_source
        if signal_set is not UNSET:
            field_dict["signal_set"] = signal_set
        if confidence is not UNSET:
            field_dict["confidence"] = confidence
        if first_seen is not UNSET:
            field_dict["first_seen"] = first_seen
        if last_seen is not UNSET:
            field_dict["last_seen"] = last_seen
        if false_positive_reason is not UNSET:
            field_dict["false_positive_reason"] = false_positive_reason
        if exception_id is not UNSET:
            field_dict["exception_id"] = exception_id
        if retention_class is not UNSET:
            field_dict["retention_class"] = retention_class

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.shadow_evidence_pointer import ShadowEvidencePointer
        from ..models.shadow_agent_finding_metadata import ShadowAgentFindingMetadata

        d = src_dict.copy()
        finding_id = d.pop("finding_id")

        tenant_id = d.pop("tenant_id")

        owner_principal_id = d.pop("owner_principal_id")

        principal_id = d.pop("principal_id")

        agent_product = d.pop("agent_product")

        risk = ShadowAgentFindingRisk(d.pop("risk"))

        status = ShadowAgentFindingStatus(d.pop("status"))

        evidence_type = d.pop("evidence_type")

        detected_at = isoparse(d.pop("detected_at"))

        created_at = isoparse(d.pop("created_at"))

        updated_at = isoparse(d.pop("updated_at"))

        agent_id = d.pop("agent_id", UNSET)

        hostname = d.pop("hostname", UNSET)

        evidence_summary = d.pop("evidence_summary", UNSET)

        _evidence_artifact_ptr = d.pop("evidence_artifact_ptr", UNSET)
        evidence_artifact_ptr: Union[Unset, ShadowEvidencePointer]
        if isinstance(_evidence_artifact_ptr, Unset):
            evidence_artifact_ptr = UNSET
        else:
            evidence_artifact_ptr = ShadowEvidencePointer.from_dict(_evidence_artifact_ptr)

        redacted_path = d.pop("redacted_path", UNSET)

        def _parse_resolved_at(data: object) -> Union[None, Unset, datetime.datetime]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                resolved_at_type_0 = isoparse(data)

                return resolved_at_type_0
            except:  # noqa: E722
                pass
            return cast(Union[None, Unset, datetime.datetime], data)

        resolved_at = _parse_resolved_at(d.pop("resolved_at", UNSET))

        resolved_by = d.pop("resolved_by", UNSET)

        resolution_reason = d.pop("resolution_reason", UNSET)

        def _parse_suppressed_until(data: object) -> Union[None, Unset, datetime.datetime]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                suppressed_until_type_0 = isoparse(data)

                return suppressed_until_type_0
            except:  # noqa: E722
                pass
            return cast(Union[None, Unset, datetime.datetime], data)

        suppressed_until = _parse_suppressed_until(d.pop("suppressed_until", UNSET))

        _metadata = d.pop("metadata", UNSET)
        metadata: Union[Unset, ShadowAgentFindingMetadata]
        if isinstance(_metadata, Unset):
            metadata = UNSET
        else:
            metadata = ShadowAgentFindingMetadata.from_dict(_metadata)

        _source_type = d.pop("source_type", UNSET)
        source_type: Union[Unset, ShadowAgentFindingSourceType]
        if isinstance(_source_type, Unset):
            source_type = UNSET
        else:
            source_type = ShadowAgentFindingSourceType(_source_type)

        source_id = d.pop("source_id", UNSET)

        cluster_id = d.pop("cluster_id", UNSET)

        namespace = d.pop("namespace", UNSET)

        workload_kind = d.pop("workload_kind", UNSET)

        workload_name = d.pop("workload_name", UNSET)

        pod_uid = d.pop("pod_uid", UNSET)

        _ci_provider = d.pop("ci_provider", UNSET)
        ci_provider: Union[Unset, ShadowAgentFindingCiProvider]
        if isinstance(_ci_provider, Unset):
            ci_provider = UNSET
        else:
            ci_provider = ShadowAgentFindingCiProvider(_ci_provider)

        repo = d.pop("repo", UNSET)

        ref = d.pop("ref", UNSET)

        workflow_id = d.pop("workflow_id", UNSET)

        job_id = d.pop("job_id", UNSET)

        run_id = d.pop("run_id", UNSET)

        runner_id = d.pop("runner_id", UNSET)

        tenant_source = d.pop("tenant_source", UNSET)

        principal_source = d.pop("principal_source", UNSET)

        signal_set = cast(List[str], d.pop("signal_set", UNSET))

        confidence = d.pop("confidence", UNSET)

        def _parse_first_seen(data: object) -> Union[None, Unset, datetime.datetime]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                first_seen_type_0 = isoparse(data)

                return first_seen_type_0
            except:  # noqa: E722
                pass
            return cast(Union[None, Unset, datetime.datetime], data)

        first_seen = _parse_first_seen(d.pop("first_seen", UNSET))

        def _parse_last_seen(data: object) -> Union[None, Unset, datetime.datetime]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                last_seen_type_0 = isoparse(data)

                return last_seen_type_0
            except:  # noqa: E722
                pass
            return cast(Union[None, Unset, datetime.datetime], data)

        last_seen = _parse_last_seen(d.pop("last_seen", UNSET))

        false_positive_reason = d.pop("false_positive_reason", UNSET)

        exception_id = d.pop("exception_id", UNSET)

        _retention_class = d.pop("retention_class", UNSET)
        retention_class: Union[Unset, ShadowAgentFindingRetentionClass]
        if isinstance(_retention_class, Unset):
            retention_class = UNSET
        else:
            retention_class = ShadowAgentFindingRetentionClass(_retention_class)

        shadow_agent_finding = cls(
            finding_id=finding_id,
            tenant_id=tenant_id,
            owner_principal_id=owner_principal_id,
            principal_id=principal_id,
            agent_product=agent_product,
            risk=risk,
            status=status,
            evidence_type=evidence_type,
            detected_at=detected_at,
            created_at=created_at,
            updated_at=updated_at,
            agent_id=agent_id,
            hostname=hostname,
            evidence_summary=evidence_summary,
            evidence_artifact_ptr=evidence_artifact_ptr,
            redacted_path=redacted_path,
            resolved_at=resolved_at,
            resolved_by=resolved_by,
            resolution_reason=resolution_reason,
            suppressed_until=suppressed_until,
            metadata=metadata,
            source_type=source_type,
            source_id=source_id,
            cluster_id=cluster_id,
            namespace=namespace,
            workload_kind=workload_kind,
            workload_name=workload_name,
            pod_uid=pod_uid,
            ci_provider=ci_provider,
            repo=repo,
            ref=ref,
            workflow_id=workflow_id,
            job_id=job_id,
            run_id=run_id,
            runner_id=runner_id,
            tenant_source=tenant_source,
            principal_source=principal_source,
            signal_set=signal_set,
            confidence=confidence,
            first_seen=first_seen,
            last_seen=last_seen,
            false_positive_reason=false_positive_reason,
            exception_id=exception_id,
            retention_class=retention_class,
        )

        shadow_agent_finding.additional_properties = d
        return shadow_agent_finding

    @property
    def additional_keys(self) -> List[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
