from typing import Any, Dict, Type, TypeVar, Tuple, Optional, BinaryIO, TextIO, TYPE_CHECKING

from typing import List


from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

from ..models.edge_session_create_request_mode import EdgeSessionCreateRequestMode
from ..models.edge_session_create_request_policy_mode import EdgeSessionCreateRequestPolicyMode
from ..models.edge_session_create_request_principal_type import (
    EdgeSessionCreateRequestPrincipalType,
)
from ..types import UNSET, Unset
from typing import cast
from typing import Dict
from typing import Union

if TYPE_CHECKING:
    from ..models.edge_enforcement_layers import EdgeEnforcementLayers
    from ..models.edge_labels import EdgeLabels


T = TypeVar("T", bound="EdgeSessionCreateRequest")


@_attrs_define
class EdgeSessionCreateRequest:
    """
    Attributes:
        tenant_id (Union[Unset, str]): Optional body tenant; when present it must match X-Tenant-ID.
        principal_id (Union[Unset, str]):
        principal_type (Union[Unset, EdgeSessionCreateRequestPrincipalType]):
        agent_product (Union[Unset, str]):
        agent_version (Union[Unset, str]):
        mode (Union[Unset, EdgeSessionCreateRequestMode]):
        repo (Union[Unset, str]):
        git_remote (Union[Unset, str]):
        git_branch (Union[Unset, str]):
        git_sha (Union[Unset, str]):
        cwd (Union[Unset, str]):
        host_id (Union[Unset, str]):
        device_id (Union[Unset, str]):
        trace_id (Union[Unset, str]):
        workflow_run_id (Union[Unset, str]):
        job_id (Union[Unset, str]):
        policy_snapshot (Union[Unset, str]): Redacted policy snapshot identifier or summary; raw secrets are redacted
            before persistence/response.
        enforcement_layers (Union[Unset, EdgeEnforcementLayers]):
        policy_mode (Union[Unset, EdgeSessionCreateRequestPolicyMode]):
        labels (Union[Unset, EdgeLabels]):
    """

    tenant_id: Union[Unset, str] = UNSET
    principal_id: Union[Unset, str] = UNSET
    principal_type: Union[Unset, EdgeSessionCreateRequestPrincipalType] = UNSET
    agent_product: Union[Unset, str] = UNSET
    agent_version: Union[Unset, str] = UNSET
    mode: Union[Unset, EdgeSessionCreateRequestMode] = UNSET
    repo: Union[Unset, str] = UNSET
    git_remote: Union[Unset, str] = UNSET
    git_branch: Union[Unset, str] = UNSET
    git_sha: Union[Unset, str] = UNSET
    cwd: Union[Unset, str] = UNSET
    host_id: Union[Unset, str] = UNSET
    device_id: Union[Unset, str] = UNSET
    trace_id: Union[Unset, str] = UNSET
    workflow_run_id: Union[Unset, str] = UNSET
    job_id: Union[Unset, str] = UNSET
    policy_snapshot: Union[Unset, str] = UNSET
    enforcement_layers: Union[Unset, "EdgeEnforcementLayers"] = UNSET
    policy_mode: Union[Unset, EdgeSessionCreateRequestPolicyMode] = UNSET
    labels: Union[Unset, "EdgeLabels"] = UNSET
    additional_properties: Dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> Dict[str, Any]:
        from ..models.edge_enforcement_layers import EdgeEnforcementLayers
        from ..models.edge_labels import EdgeLabels

        tenant_id = self.tenant_id

        principal_id = self.principal_id

        principal_type: Union[Unset, str] = UNSET
        if not isinstance(self.principal_type, Unset):
            principal_type = self.principal_type.value

        agent_product = self.agent_product

        agent_version = self.agent_version

        mode: Union[Unset, str] = UNSET
        if not isinstance(self.mode, Unset):
            mode = self.mode.value

        repo = self.repo

        git_remote = self.git_remote

        git_branch = self.git_branch

        git_sha = self.git_sha

        cwd = self.cwd

        host_id = self.host_id

        device_id = self.device_id

        trace_id = self.trace_id

        workflow_run_id = self.workflow_run_id

        job_id = self.job_id

        policy_snapshot = self.policy_snapshot

        enforcement_layers: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.enforcement_layers, Unset):
            enforcement_layers = self.enforcement_layers.to_dict()

        policy_mode: Union[Unset, str] = UNSET
        if not isinstance(self.policy_mode, Unset):
            policy_mode = self.policy_mode.value

        labels: Union[Unset, Dict[str, Any]] = UNSET
        if not isinstance(self.labels, Unset):
            labels = self.labels.to_dict()

        field_dict: Dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if tenant_id is not UNSET:
            field_dict["tenant_id"] = tenant_id
        if principal_id is not UNSET:
            field_dict["principal_id"] = principal_id
        if principal_type is not UNSET:
            field_dict["principal_type"] = principal_type
        if agent_product is not UNSET:
            field_dict["agent_product"] = agent_product
        if agent_version is not UNSET:
            field_dict["agent_version"] = agent_version
        if mode is not UNSET:
            field_dict["mode"] = mode
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
        if trace_id is not UNSET:
            field_dict["trace_id"] = trace_id
        if workflow_run_id is not UNSET:
            field_dict["workflow_run_id"] = workflow_run_id
        if job_id is not UNSET:
            field_dict["job_id"] = job_id
        if policy_snapshot is not UNSET:
            field_dict["policy_snapshot"] = policy_snapshot
        if enforcement_layers is not UNSET:
            field_dict["enforcement_layers"] = enforcement_layers
        if policy_mode is not UNSET:
            field_dict["policy_mode"] = policy_mode
        if labels is not UNSET:
            field_dict["labels"] = labels

        return field_dict

    @classmethod
    def from_dict(cls: Type[T], src_dict: Dict[str, Any]) -> T:
        from ..models.edge_enforcement_layers import EdgeEnforcementLayers
        from ..models.edge_labels import EdgeLabels

        d = src_dict.copy()
        tenant_id = d.pop("tenant_id", UNSET)

        principal_id = d.pop("principal_id", UNSET)

        _principal_type = d.pop("principal_type", UNSET)
        principal_type: Union[Unset, EdgeSessionCreateRequestPrincipalType]
        if isinstance(_principal_type, Unset):
            principal_type = UNSET
        else:
            principal_type = EdgeSessionCreateRequestPrincipalType(_principal_type)

        agent_product = d.pop("agent_product", UNSET)

        agent_version = d.pop("agent_version", UNSET)

        _mode = d.pop("mode", UNSET)
        mode: Union[Unset, EdgeSessionCreateRequestMode]
        if isinstance(_mode, Unset):
            mode = UNSET
        else:
            mode = EdgeSessionCreateRequestMode(_mode)

        repo = d.pop("repo", UNSET)

        git_remote = d.pop("git_remote", UNSET)

        git_branch = d.pop("git_branch", UNSET)

        git_sha = d.pop("git_sha", UNSET)

        cwd = d.pop("cwd", UNSET)

        host_id = d.pop("host_id", UNSET)

        device_id = d.pop("device_id", UNSET)

        trace_id = d.pop("trace_id", UNSET)

        workflow_run_id = d.pop("workflow_run_id", UNSET)

        job_id = d.pop("job_id", UNSET)

        policy_snapshot = d.pop("policy_snapshot", UNSET)

        _enforcement_layers = d.pop("enforcement_layers", UNSET)
        enforcement_layers: Union[Unset, EdgeEnforcementLayers]
        if isinstance(_enforcement_layers, Unset):
            enforcement_layers = UNSET
        else:
            enforcement_layers = EdgeEnforcementLayers.from_dict(_enforcement_layers)

        _policy_mode = d.pop("policy_mode", UNSET)
        policy_mode: Union[Unset, EdgeSessionCreateRequestPolicyMode]
        if isinstance(_policy_mode, Unset):
            policy_mode = UNSET
        else:
            policy_mode = EdgeSessionCreateRequestPolicyMode(_policy_mode)

        _labels = d.pop("labels", UNSET)
        labels: Union[Unset, EdgeLabels]
        if isinstance(_labels, Unset):
            labels = UNSET
        else:
            labels = EdgeLabels.from_dict(_labels)

        edge_session_create_request = cls(
            tenant_id=tenant_id,
            principal_id=principal_id,
            principal_type=principal_type,
            agent_product=agent_product,
            agent_version=agent_version,
            mode=mode,
            repo=repo,
            git_remote=git_remote,
            git_branch=git_branch,
            git_sha=git_sha,
            cwd=cwd,
            host_id=host_id,
            device_id=device_id,
            trace_id=trace_id,
            workflow_run_id=workflow_run_id,
            job_id=job_id,
            policy_snapshot=policy_snapshot,
            enforcement_layers=enforcement_layers,
            policy_mode=policy_mode,
            labels=labels,
        )

        edge_session_create_request.additional_properties = d
        return edge_session_create_request

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
