from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.edge_session_mode import EdgeSessionMode
from ..models.edge_session_policy_mode import EdgeSessionPolicyMode
from ..models.edge_session_principal_type import EdgeSessionPrincipalType
from ..models.edge_session_status import EdgeSessionStatus
from ..types import UNSET, Unset
from dateutil.parser import isoparse
from typing import cast
from typing import cast, Union
from typing import Dict
from typing import Union
import datetime

if TYPE_CHECKING:
    from ..models.edge_enforcement_layers import EdgeEnforcementLayers
    from ..models.edge_risk_summary import EdgeRiskSummary
    from ..models.edge_labels import EdgeLabels


T = TypeVar("T", bound="EdgeSession")


@_attrs_define
class EdgeSession:
    """
    Attributes:
        session_id (str):
        tenant_id (str):
        principal_type (EdgeSessionPrincipalType):
        mode (EdgeSessionMode):
        trace_id (str):
        policy_mode (EdgeSessionPolicyMode):
        status (EdgeSessionStatus):
        risk_summary (EdgeRiskSummary):
        started_at (datetime.datetime):
        principal_id (Union[Unset, str]):
        agent_product (Union[Unset, str]):
        agent_version (Union[Unset, str]):
        repo (Union[Unset, str]):
        git_remote (Union[Unset, str]):
        git_branch (Union[Unset, str]):
        git_sha (Union[Unset, str]):
        cwd (Union[Unset, str]):
        host_id (Union[Unset, str]):
        device_id (Union[Unset, str]):
        workflow_run_id (Union[Unset, str]):
        job_id (Union[Unset, str]):
        policy_snapshot (Union[Unset, str]): Redacted policy snapshot identifier or summary; raw secrets are redacted
            before persistence/response.
        enforcement_layers (Union[Unset, EdgeEnforcementLayers]):
        ended_at (Union[None, Unset, datetime.datetime]):
        labels (Union[Unset, EdgeLabels]):
    """

    session_id: str
    tenant_id: str
    principal_type: EdgeSessionPrincipalType
    mode: EdgeSessionMode
    trace_id: str
    policy_mode: EdgeSessionPolicyMode
    status: EdgeSessionStatus
    risk_summary: "EdgeRiskSummary"
    started_at: datetime.datetime
    principal_id: Union[Unset, str] = UNSET
    agent_product: Union[Unset, str] = UNSET
    agent_version: Union[Unset, str] = UNSET
    repo: Union[Unset, str] = UNSET
    git_remote: Union[Unset, str] = UNSET
    git_branch: Union[Unset, str] = UNSET
    git_sha: Union[Unset, str] = UNSET
    cwd: Union[Unset, str] = UNSET
    host_id: Union[Unset, str] = UNSET
    device_id: Union[Unset, str] = UNSET
    workflow_run_id: Union[Unset, str] = UNSET
    job_id: Union[Unset, str] = UNSET
    policy_snapshot: Union[Unset, str] = UNSET
    enforcement_layers: Union[Unset, "EdgeEnforcementLayers"] = UNSET
    ended_at: Union[None, Unset, datetime.datetime] = UNSET
    labels: Union[Unset, "EdgeLabels"] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.edge_enforcement_layers import EdgeEnforcementLayers
        from ..models.edge_risk_summary import EdgeRiskSummary
        from ..models.edge_labels import EdgeLabels

        session_id = self.session_id

        tenant_id = self.tenant_id

        principal_type = self.principal_type.value

        mode = self.mode.value

        trace_id = self.trace_id

        policy_mode = self.policy_mode.value

        status = self.status.value

        risk_summary = self.risk_summary.to_dict()

        started_at = self.started_at.isoformat()

        principal_id = self.principal_id

        agent_product = self.agent_product

        agent_version = self.agent_version

        repo = self.repo

        git_remote = self.git_remote

        git_branch = self.git_branch

        git_sha = self.git_sha

        cwd = self.cwd

        host_id = self.host_id

        device_id = self.device_id

        workflow_run_id = self.workflow_run_id

        job_id = self.job_id

        policy_snapshot = self.policy_snapshot

        enforcement_layers: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.enforcement_layers, Unset):
            enforcement_layers = self.enforcement_layers.to_dict()

        ended_at: Union[None, Unset, str]
        if isinstance(self.ended_at, Unset):
            ended_at = UNSET
        elif isinstance(self.ended_at, datetime.datetime):
            ended_at = self.ended_at.isoformat()
        else:
            ended_at = self.ended_at

        labels: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.labels, Unset):
            labels = self.labels.to_dict()

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "session_id": session_id,
                "tenant_id": tenant_id,
                "principal_type": principal_type,
                "mode": mode,
                "trace_id": trace_id,
                "policy_mode": policy_mode,
                "status": status,
                "risk_summary": risk_summary,
                "started_at": started_at,
            }
        )
        if principal_id is not UNSET:
            field_dict["principal_id"] = principal_id
        if agent_product is not UNSET:
            field_dict["agent_product"] = agent_product
        if agent_version is not UNSET:
            field_dict["agent_version"] = agent_version
        if repo is not UNSET:
            field_dict["repo"] = repo
        if git_remote is not UNSET:
            field_dict["git_remote"] = git_remote
        if git_branch is not UNSET:
            field_dict["git_branch"] = git_branch
        if git_sha is not UNSET:
            field_dict["git_sha"] = git_sha
        if cwd is not UNSET:
            field_dict["cwd"] = cwd
        if host_id is not UNSET:
            field_dict["host_id"] = host_id
        if device_id is not UNSET:
            field_dict["device_id"] = device_id
        if workflow_run_id is not UNSET:
            field_dict["workflow_run_id"] = workflow_run_id
        if job_id is not UNSET:
            field_dict["job_id"] = job_id
        if policy_snapshot is not UNSET:
            field_dict["policy_snapshot"] = policy_snapshot
        if enforcement_layers is not UNSET:
            field_dict["enforcement_layers"] = enforcement_layers
        if ended_at is not UNSET:
            field_dict["ended_at"] = ended_at
        if labels is not UNSET:
            field_dict["labels"] = labels

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.edge_enforcement_layers import EdgeEnforcementLayers
        from ..models.edge_risk_summary import EdgeRiskSummary
        from ..models.edge_labels import EdgeLabels

        d = src_dict.copy()
        session_id = d.pop("session_id")

        tenant_id = d.pop("tenant_id")

        principal_type = EdgeSessionPrincipalType(d.pop("principal_type"))

        mode = EdgeSessionMode(d.pop("mode"))

        trace_id = d.pop("trace_id")

        policy_mode = EdgeSessionPolicyMode(d.pop("policy_mode"))

        status = EdgeSessionStatus(d.pop("status"))

        risk_summary = EdgeRiskSummary.from_dict(d.pop("risk_summary"))

        started_at = isoparse(d.pop("started_at"))

        principal_id = d.pop("principal_id", UNSET)

        agent_product = d.pop("agent_product", UNSET)

        agent_version = d.pop("agent_version", UNSET)

        repo = d.pop("repo", UNSET)

        git_remote = d.pop("git_remote", UNSET)

        git_branch = d.pop("git_branch", UNSET)

        git_sha = d.pop("git_sha", UNSET)

        cwd = d.pop("cwd", UNSET)

        host_id = d.pop("host_id", UNSET)

        device_id = d.pop("device_id", UNSET)

        workflow_run_id = d.pop("workflow_run_id", UNSET)

        job_id = d.pop("job_id", UNSET)

        policy_snapshot = d.pop("policy_snapshot", UNSET)

        _enforcement_layers = d.pop("enforcement_layers", UNSET)
        enforcement_layers: Union[Unset, EdgeEnforcementLayers]
        if isinstance(_enforcement_layers, Unset):
            enforcement_layers = UNSET
        else:
            enforcement_layers = EdgeEnforcementLayers.from_dict(_enforcement_layers)

        def _parse_ended_at(data: object) -> Union[None, Unset, datetime.datetime]:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                ended_at_type_0 = isoparse(data)

                return ended_at_type_0
            except:  # noqa: E722
                pass
            return cast(Union[None, Unset, datetime.datetime], data)

        ended_at = _parse_ended_at(d.pop("ended_at", UNSET))

        _labels = d.pop("labels", UNSET)
        labels: Union[Unset, EdgeLabels]
        if isinstance(_labels, Unset):
            labels = UNSET
        else:
            labels = EdgeLabels.from_dict(_labels)

        edge_session = cls(
            session_id=session_id,
            tenant_id=tenant_id,
            principal_type=principal_type,
            mode=mode,
            trace_id=trace_id,
            policy_mode=policy_mode,
            status=status,
            risk_summary=risk_summary,
            started_at=started_at,
            principal_id=principal_id,
            agent_product=agent_product,
            agent_version=agent_version,
            repo=repo,
            git_remote=git_remote,
            git_branch=git_branch,
            git_sha=git_sha,
            cwd=cwd,
            host_id=host_id,
            device_id=device_id,
            workflow_run_id=workflow_run_id,
            job_id=job_id,
            policy_snapshot=policy_snapshot,
            enforcement_layers=enforcement_layers,
            ended_at=ended_at,
            labels=labels,
        )

        edge_session.additional_properties = d
        return edge_session

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
