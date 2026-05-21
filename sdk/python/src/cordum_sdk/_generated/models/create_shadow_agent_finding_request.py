from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.create_shadow_agent_finding_request_ci_provider import (
    CreateShadowAgentFindingRequestCiProvider,
)
from ..models.create_shadow_agent_finding_request_retention_class import (
    CreateShadowAgentFindingRequestRetentionClass,
)
from ..models.create_shadow_agent_finding_request_risk import CreateShadowAgentFindingRequestRisk
from ..models.create_shadow_agent_finding_request_source_type import (
    CreateShadowAgentFindingRequestSourceType,
)
from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import cast, List
from typing import Dict
from typing import Union
import datetime

if TYPE_CHECKING:
    from ..models.shadow_evidence_pointer import ShadowEvidencePointer
    from ..models.create_shadow_agent_finding_request_metadata import (
        CreateShadowAgentFindingRequestMetadata,
    )


T = TypeVar("T", bound="CreateShadowAgentFindingRequest")


@_attrs_define
class CreateShadowAgentFindingRequest:
    """
    Attributes:
        owner_principal_id (str):
        agent_product (str):
        risk (CreateShadowAgentFindingRequestRisk):
        evidence_type (str):
        detected_at (datetime.datetime):
        finding_id (Union[Unset, str]): Optional caller-supplied id. When omitted, server generates with `edge_shadow_`
            prefix.
        tenant_id (Union[Unset, str]): Optional, must match X-Tenant-ID header when supplied.
        principal_id (Union[Unset, str]): Detector identity; falls back to authenticated caller.
        agent_id (Union[Unset, str]):
        hostname (Union[Unset, str]):
        evidence_summary (Union[Unset, str]):
        evidence_artifact_ptr (Union[Unset, ShadowEvidencePointer]): Reference to redacted evidence stored outside the
            finding record. Distinct from `ArtifactPointer` because shadow findings have no session/execution context.
        redacted_path (Union[Unset, str]):
        metadata (Union[Unset, CreateShadowAgentFindingRequestMetadata]):
        source_type (Union[Unset, CreateShadowAgentFindingRequestSourceType]): EDGE-143.5 §10.1; defaults to `local`
            when omitted.
        source_id (Union[Unset, str]):
        cluster_id (Union[Unset, str]):
        namespace (Union[Unset, str]):
        workload_kind (Union[Unset, str]):
        workload_name (Union[Unset, str]):
        pod_uid (Union[Unset, str]):
        ci_provider (Union[Unset, CreateShadowAgentFindingRequestCiProvider]):
        repo (Union[Unset, str]):
        ref (Union[Unset, str]):
        workflow_id (Union[Unset, str]):
        job_id (Union[Unset, str]):
        run_id (Union[Unset, str]):
        runner_id (Union[Unset, str]):
        tenant_source (Union[Unset, str]):
        principal_source (Union[Unset, str]):
        signal_set (Union[Unset, List[str]]):
        confidence (Union[Unset, float]):
        first_seen (Union[Unset, datetime.datetime]):
        last_seen (Union[Unset, datetime.datetime]):
        false_positive_reason (Union[Unset, str]):
        exception_id (Union[Unset, str]):
        retention_class (Union[Unset, CreateShadowAgentFindingRequestRetentionClass]):
    """

    owner_principal_id: str
    agent_product: str
    risk: CreateShadowAgentFindingRequestRisk
    evidence_type: str
    detected_at: datetime.datetime
    finding_id: Union[Unset, str] = UNSET
    tenant_id: Union[Unset, str] = UNSET
    principal_id: Union[Unset, str] = UNSET
    agent_id: Union[Unset, str] = UNSET
    hostname: Union[Unset, str] = UNSET
    evidence_summary: Union[Unset, str] = UNSET
    evidence_artifact_ptr: Union[Unset, "ShadowEvidencePointer"] = UNSET
    redacted_path: Union[Unset, str] = UNSET
    metadata: Union[Unset, "CreateShadowAgentFindingRequestMetadata"] = UNSET
    source_type: Union[Unset, CreateShadowAgentFindingRequestSourceType] = UNSET
    source_id: Union[Unset, str] = UNSET
    cluster_id: Union[Unset, str] = UNSET
    namespace: Union[Unset, str] = UNSET
    workload_kind: Union[Unset, str] = UNSET
    workload_name: Union[Unset, str] = UNSET
    pod_uid: Union[Unset, str] = UNSET
    ci_provider: Union[Unset, CreateShadowAgentFindingRequestCiProvider] = UNSET
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
    first_seen: Union[Unset, datetime.datetime] = UNSET
    last_seen: Union[Unset, datetime.datetime] = UNSET
    false_positive_reason: Union[Unset, str] = UNSET
    exception_id: Union[Unset, str] = UNSET
    retention_class: Union[Unset, CreateShadowAgentFindingRequestRetentionClass] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.shadow_evidence_pointer import ShadowEvidencePointer
        from ..models.create_shadow_agent_finding_request_metadata import (
            CreateShadowAgentFindingRequestMetadata,
        )

        owner_principal_id = self.owner_principal_id

        agent_product = self.agent_product

        risk = self.risk.value

        evidence_type = self.evidence_type

        detected_at = self.detected_at.isoformat()

        finding_id = self.finding_id

        tenant_id = self.tenant_id

        principal_id = self.principal_id

        agent_id = self.agent_id

        hostname = self.hostname

        evidence_summary = self.evidence_summary

        evidence_artifact_ptr: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.evidence_artifact_ptr, Unset):
            evidence_artifact_ptr = self.evidence_artifact_ptr.to_dict()

        redacted_path = self.redacted_path

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

        first_seen: Union[Unset, str] = UNSET
        if not isinstance(self.first_seen, Unset):
            first_seen = self.first_seen.isoformat()

        last_seen: Union[Unset, str] = UNSET
        if not isinstance(self.last_seen, Unset):
            last_seen = self.last_seen.isoformat()

        false_positive_reason = self.false_positive_reason

        exception_id = self.exception_id

        retention_class: Union[Unset, str] = UNSET
        if not isinstance(self.retention_class, Unset):
            retention_class = self.retention_class.value

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "owner_principal_id": owner_principal_id,
                "agent_product": agent_product,
                "risk": risk,
                "evidence_type": evidence_type,
                "detected_at": detected_at,
            }
        )
        if finding_id is not UNSET:
            field_dict["finding_id"] = finding_id
        if tenant_id is not UNSET:
            field_dict["tenant_id"] = tenant_id
        if principal_id is not UNSET:
            field_dict["principal_id"] = principal_id
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
        from ..models.create_shadow_agent_finding_request_metadata import (
            CreateShadowAgentFindingRequestMetadata,
        )

        d = src_dict.copy()
        owner_principal_id = d.pop("owner_principal_id")

        agent_product = d.pop("agent_product")

        risk = CreateShadowAgentFindingRequestRisk(d.pop("risk"))

        evidence_type = d.pop("evidence_type")

        detected_at = isoparse(d.pop("detected_at"))

        finding_id = d.pop("finding_id", UNSET)

        tenant_id = d.pop("tenant_id", UNSET)

        principal_id = d.pop("principal_id", UNSET)

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

        _metadata = d.pop("metadata", UNSET)
        metadata: Union[Unset, CreateShadowAgentFindingRequestMetadata]
        if isinstance(_metadata, Unset):
            metadata = UNSET
        else:
            metadata = CreateShadowAgentFindingRequestMetadata.from_dict(_metadata)

        _source_type = d.pop("source_type", UNSET)
        source_type: Union[Unset, CreateShadowAgentFindingRequestSourceType]
        if isinstance(_source_type, Unset):
            source_type = UNSET
        else:
            source_type = CreateShadowAgentFindingRequestSourceType(_source_type)

        source_id = d.pop("source_id", UNSET)

        cluster_id = d.pop("cluster_id", UNSET)

        namespace = d.pop("namespace", UNSET)

        workload_kind = d.pop("workload_kind", UNSET)

        workload_name = d.pop("workload_name", UNSET)

        pod_uid = d.pop("pod_uid", UNSET)

        _ci_provider = d.pop("ci_provider", UNSET)
        ci_provider: Union[Unset, CreateShadowAgentFindingRequestCiProvider]
        if isinstance(_ci_provider, Unset):
            ci_provider = UNSET
        else:
            ci_provider = CreateShadowAgentFindingRequestCiProvider(_ci_provider)

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

        _first_seen = d.pop("first_seen", UNSET)
        first_seen: Union[Unset, datetime.datetime]
        if isinstance(_first_seen, Unset):
            first_seen = UNSET
        else:
            first_seen = isoparse(_first_seen)

        _last_seen = d.pop("last_seen", UNSET)
        last_seen: Union[Unset, datetime.datetime]
        if isinstance(_last_seen, Unset):
            last_seen = UNSET
        else:
            last_seen = isoparse(_last_seen)

        false_positive_reason = d.pop("false_positive_reason", UNSET)

        exception_id = d.pop("exception_id", UNSET)

        _retention_class = d.pop("retention_class", UNSET)
        retention_class: Union[Unset, CreateShadowAgentFindingRequestRetentionClass]
        if isinstance(_retention_class, Unset):
            retention_class = UNSET
        else:
            retention_class = CreateShadowAgentFindingRequestRetentionClass(_retention_class)

        create_shadow_agent_finding_request = cls(
            owner_principal_id=owner_principal_id,
            agent_product=agent_product,
            risk=risk,
            evidence_type=evidence_type,
            detected_at=detected_at,
            finding_id=finding_id,
            tenant_id=tenant_id,
            principal_id=principal_id,
            agent_id=agent_id,
            hostname=hostname,
            evidence_summary=evidence_summary,
            evidence_artifact_ptr=evidence_artifact_ptr,
            redacted_path=redacted_path,
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

        create_shadow_agent_finding_request.additional_properties = d
        return create_shadow_agent_finding_request

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
