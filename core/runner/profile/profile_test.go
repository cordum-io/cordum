package profile

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/cordum/cordum/core/infra/config"
	"github.com/stretchr/testify/require"
)

func unmappedSandboxFields(typ reflect.Type) []string {
	mapped := map[string]string{
		"Isolated": "mandatory Requirements floor", "NetworkAllowlist": "network.allow",
		"FsReadOnly": "filesystem.read_only_inputs", "FsReadWrite": "filesystem.workspace_paths",
	}
	var missing []string
	for i := 0; i < typ.NumField(); i++ {
		if mapped[typ.Field(i).Name] == "" {
			missing = append(missing, typ.Field(i).Name)
		}
	}
	return missing
}

func TestSandboxFieldsAllMapped(t *testing.T) {
	require.Empty(t, unmappedSandboxFields(reflect.TypeFor[config.SandboxProfile]()))
	type futureSandbox struct {
		Isolated                                  bool
		NetworkAllowlist, FsReadOnly, FsReadWrite []string
		Extra                                     bool
	}
	require.Equal(t, []string{"Extra"}, unmappedSandboxFields(reflect.TypeFor[futureSandbox]()))
}

func TestFromSandboxIsolatedFalseKeepsFloor(t *testing.T) {
	off, err := FromSandbox(config.SandboxProfile{})
	require.NoError(t, err)
	on, err := FromSandbox(config.SandboxProfile{Isolated: true})
	require.NoError(t, err)
	require.Equal(t, on.Requirements(), off.Requirements())
	require.Len(t, off.Requirements(), 18)
	require.Equal(t, NetworkNone, off.Network.Mode)
}

func TestFromSandboxMapsFields(t *testing.T) {
	s := config.SandboxProfile{NetworkAllowlist: []string{"example.com"}, FsReadOnly: []string{"/inputs"}, FsReadWrite: []string{"/workspace"}}
	p, err := FromSandbox(s)
	require.NoError(t, err)
	require.Equal(t, Profile{SchemaVersion: SchemaVersion,
		Filesystem: Filesystem{ReadOnlyInputs: []string{"/inputs"}, WorkspacePaths: []string{"/workspace"}},
		Network:    Network{Mode: NetworkAllowlist, Allow: []string{"example.com"}},
		Runtime:    Runtime{Name: "runsc", Platform: "systrap"}}, p)
	s.NetworkAllowlist[0], s.FsReadOnly[0], s.FsReadWrite[0] = "changed", "/changed", "/changed"
	require.Equal(t, "example.com", p.Network.Allow[0])
	require.Equal(t, "/inputs", p.Filesystem.ReadOnlyInputs[0])
	require.Equal(t, "/workspace", p.Filesystem.WorkspacePaths[0])
}

func TestParseRejectsUnknownVersion(t *testing.T) {
	for _, data := range []string{`{"schema_version":2}`, `{}`, `null`} {
		p, err := Parse([]byte(data))
		require.ErrorIs(t, err, ErrUnsupportedVersion)
		require.NotErrorIs(t, err, ErrUnknownField)
		require.Equal(t, Profile{}, p)
	}
}

func TestParseRejectsUnknownField(t *testing.T) {
	for _, data := range []string{`{"schema_version":1,"extra":true}`, `{"schema_version":1,"network":{"extra":true}}`} {
		p, err := Parse([]byte(data))
		require.ErrorIs(t, err, ErrUnknownField)
		require.NotErrorIs(t, err, ErrUnsupportedVersion)
		require.Equal(t, Profile{}, p)
	}
}

func TestParseRejectsMalformedOrTrailingData(t *testing.T) {
	for _, data := range []string{"", `{"schema_version":"1"}`, `{"schema_version":1} {}`, `{"schema_version":1} trailing`} {
		p, err := Parse([]byte(data))
		require.Error(t, err)
		require.Equal(t, Profile{}, p)
	}
}

func TestParseV1(t *testing.T) {
	data := `{"schema_version":1,"filesystem":{"read_only_inputs":["/in"],"workspace_paths":["/work"],"workspace_bytes":10,"workspace_inodes":2},"network":{"mode":"allowlist","allow":["example.com"]},"resources":{"cpu_millicores":100,"memory_bytes":200,"swap_bytes":0},"processes":{"host_pids_max":3,"guest_task_ceiling":4},"syscalls":{"policy_id":"pinned"},"runtime":{"name":"runsc","platform":"systrap","release":"pinned","binary_path":"/runsc","binary_sha256":"digest"}}`
	p, err := Parse([]byte(data + "\n "))
	require.NoError(t, err)
	require.Equal(t, Profile{SchemaVersion: SchemaVersion,
		Filesystem: Filesystem{ReadOnlyInputs: []string{"/in"}, WorkspacePaths: []string{"/work"}, WorkspaceBytes: 10, WorkspaceInodes: 2},
		Network:    Network{Mode: NetworkAllowlist, Allow: []string{"example.com"}}, Resources: Resources{CPUMillicores: 100, MemoryBytes: 200},
		Processes: Processes{HostPidsMax: 3, GuestTaskCeiling: 4}, Syscalls: Syscalls{PolicyID: "pinned"},
		Runtime: Runtime{Name: "runsc", Platform: "systrap", Release: "pinned", BinaryPath: "/runsc", BinarySHA256: "digest"}}, p)
	encoded, err := json.Marshal(p)
	require.NoError(t, err)
	require.JSONEq(t, data, string(encoded))
}

func TestRequirementsCoverRefusalItems(t *testing.T) {
	items := [][]Capability{
		{CapKernelFloor, CapNamespaces, CapSeccompTrap, CapNoNewPrivs, CapRuntimeBinary, CapSystrap},
		{CapCgroupV2, CapCgroupControllers, CapCgroupGroupOOM, CapCgroupKill, CapCloneIntoCgroup},
		{CapGuestTaskCeiling}, {CapInputMaterialization, CapWorkspaceQuota}, {CapOutputBudget},
		{CapWorkloadIsolation}, {CapNetNamespace, CapNetDefaultDeny}, {CapGuestSyscallPolicy},
	}
	floor := (Profile{}).Requirements()
	all := AllCapabilities()
	require.Len(t, all, 19)
	require.True(t, slices.IsSorted(all))
	require.True(t, slices.IsSorted(floor))
	var want []Capability
	for bullet, ids := range items {
		for _, id := range ids {
			want = append(want, id)
			if id != CapNetDefaultDeny {
				require.Contains(t, floor, id, "ADR-012 bullet %d", bullet+1)
			}
		}
	}
	require.ElementsMatch(t, want, all)
	require.Equal(t, []Capability{
		"cgroup.controllers", "cgroup.group_oom", "cgroup.kill", "cgroup.v2_unified",
		"fs.input_materialization", "fs.workspace_quota", "guest.syscall_policy", "guest.task_ceiling",
		"kernel.clone_into_cgroup", "kernel.floor", "kernel.namespaces", "kernel.no_new_privs", "kernel.seccomp_trap",
		"net.default_deny", "net.namespace", "output.byte_budget", "proc.workload_isolation", "runtime.runsc_binary", "runtime.systrap",
	}, all)
	require.NotContains(t, floor, CapNetDefaultDeny)
	require.Equal(t, all, (Profile{Network: Network{Mode: NetworkAllowlist}}).Requirements())
	all[0] = "mutated"
	require.NotContains(t, AllCapabilities(), Capability("mutated"))
}

func TestProbeResultJSON(t *testing.T) {
	for _, status := range []Status{StatusAvailable, StatusUnavailable, StatusUnproven} {
		require.Contains(t, []string{"available", "unavailable", "unproven"}, string(status))
	}
	result := ProbeResult{ProbedAt: time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC), Host: "reference",
		Capabilities: map[Capability]CapabilityResult{CapKernelFloor: {Status: StatusUnproven, Detail: "not probed"}}}
	data, err := json.Marshal(result)
	require.NoError(t, err)
	require.JSONEq(t, `{"probed_at":"2026-11-17T00:00:00Z","host":"reference","capabilities":{"kernel.floor":{"status":"unproven","detail":"not probed"}}}`, string(data))
}

func TestFromSandboxRejectsInvalidEntries(t *testing.T) {
	for _, value := range []string{"", "relative", "/a/../b", "/a//b", "/a/", "/a\x00b", `C:\inputs`} {
		for _, s := range []config.SandboxProfile{{FsReadOnly: []string{value}}, {FsReadWrite: []string{value}}} {
			p, err := FromSandbox(s)
			require.ErrorIs(t, err, ErrInvalidSandbox, "%q", value)
			require.Equal(t, Profile{}, p)
		}
	}
	for _, value := range []string{"", " ", " example.com", "*.example.com", "ex?mple.com", "[ab].example.com"} {
		p, err := FromSandbox(config.SandboxProfile{NetworkAllowlist: []string{value}})
		require.ErrorIs(t, err, ErrInvalidSandbox, "%q", value)
		require.Equal(t, Profile{}, p)
	}
}
