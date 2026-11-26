package profile

import (
	"context"
	"slices"
	"time"
)

type Capability string

const (
	CapKernelFloor          Capability = "kernel.floor"
	CapNamespaces           Capability = "kernel.namespaces"
	CapSeccompTrap          Capability = "kernel.seccomp_trap"
	CapNoNewPrivs           Capability = "kernel.no_new_privs"
	CapRuntimeBinary        Capability = "runtime.runsc_binary"
	CapSystrap              Capability = "runtime.systrap"
	CapCgroupV2             Capability = "cgroup.v2_unified"
	CapCgroupControllers    Capability = "cgroup.controllers"
	CapCgroupGroupOOM       Capability = "cgroup.group_oom"
	CapCgroupKill           Capability = "cgroup.kill"
	CapCloneIntoCgroup      Capability = "kernel.clone_into_cgroup"
	CapGuestTaskCeiling     Capability = "guest.task_ceiling"
	CapInputMaterialization Capability = "fs.input_materialization"
	CapWorkspaceQuota       Capability = "fs.workspace_quota" // #nosec G101 -- capability wire id, not a credential
	CapOutputBudget         Capability = "output.byte_budget"
	CapWorkloadIsolation    Capability = "proc.workload_isolation" // #nosec G101 -- capability wire id, not a credential
	CapNetNamespace         Capability = "net.namespace"
	CapNetDefaultDeny       Capability = "net.default_deny"
	CapGuestSyscallPolicy   Capability = "guest.syscall_policy"
)

// AllCapabilities returns a fresh, sorted list of capability wire identifiers.
func AllCapabilities() []Capability {
	caps := []Capability{
		CapKernelFloor, CapNamespaces, CapSeccompTrap, CapNoNewPrivs, CapRuntimeBinary, CapSystrap,
		CapCgroupV2, CapCgroupControllers, CapCgroupGroupOOM, CapCgroupKill, CapCloneIntoCgroup,
		CapGuestTaskCeiling, CapInputMaterialization, CapWorkspaceQuota, CapOutputBudget,
		CapWorkloadIsolation, CapNetNamespace, CapNetDefaultDeny, CapGuestSyscallPolicy,
	}
	slices.Sort(caps)
	return caps
}

// Requirements never drops the managed-runner floor, even for a zero Profile.
// Routed allowlist networking additionally requires default-deny rules.
func (p Profile) Requirements() []Capability {
	return slices.DeleteFunc(AllCapabilities(), func(cap Capability) bool {
		return cap == CapNetDefaultDeny && p.Network.Mode != NetworkAllowlist
	})
}

type Status string

const (
	StatusAvailable   Status = "available"
	StatusUnavailable Status = "unavailable"
	StatusUnproven    Status = "unproven"
)

type CapabilityResult struct {
	Status Status `json:"status"`
	Detail string `json:"detail"`
}

type ProbeResult struct {
	ProbedAt     time.Time                       `json:"probed_at"`
	Host         string                          `json:"host"`
	Capabilities map[Capability]CapabilityResult `json:"capabilities"`
}

type Prober interface {
	Probe(ctx context.Context, want Profile) (ProbeResult, error)
}
