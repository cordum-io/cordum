//go:build linux

package profile

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

const v1Mount = "40 30 0:30 / /sys/fs/cgroup/memory rw - cgroup cgroup rw,memory"

var fakeRootCases = []struct {
	name     string
	override map[string]string
	cap      Capability
	want     Status
}{
	{"cgroup v1 host", map[string]string{"proc/self/mountinfo": v1Mount}, CapCgroupV2, StatusUnavailable},
	{"hybrid host", map[string]string{"proc/self/mountinfo": "30 1 0:26 / /sys/fs/cgroup/unified rw - cgroup2 cgroup2 rw\n" + v1Mount}, CapCgroupV2, StatusUnavailable},
	{"cgroup2 root plus a v1 mount", map[string]string{"proc/self/mountinfo": "30 1 0:26 / /sys/fs/cgroup rw - cgroup2 cgroup2 rw\n" + v1Mount}, CapCgroupV2, StatusUnavailable},
	{"pids controller missing", map[string]string{"sys/fs/cgroup/runner/cgroup.controllers": "cpu memory"}, CapCgroupControllers, StatusUnavailable},
	{"kernel 6.7", map[string]string{"proc/sys/kernel/osrelease": "6.7.0"}, CapKernelFloor, StatusUnavailable},
	{"runsc hash differs", map[string]string{"usr/local/bin/runsc": "tampered"}, CapRuntimeBinary, StatusUnavailable},
	{"runsc missing", map[string]string{"usr/local/bin/runsc": ""}, CapRuntimeBinary, StatusUnavailable},
	{"user namespaces off", map[string]string{"proc/sys/user/max_user_namespaces": "0"}, CapNamespaces, StatusUnavailable},
	{"apparmor restricts userns", map[string]string{"proc/sys/kernel/apparmor_restrict_unprivileged_userns": "1"}, CapNamespaces, StatusUnproven},
	{"no seccomp trap", map[string]string{"proc/sys/kernel/seccomp/actions_avail": "kill_process errno allow"}, CapSeccompTrap, StatusUnavailable},
	{"no cgroup.kill", map[string]string{"sys/fs/cgroup/runner/cgroup.kill": ""}, CapCgroupKill, StatusUnavailable},
	{"no v2 cgroup entry", map[string]string{"proc/self/cgroup": "1:name=systemd:/"}, CapCgroupKill, StatusUnavailable},
	{"cgroup outside namespace", map[string]string{"proc/self/cgroup": "0::/../runner"}, CapCgroupControllers, StatusUnavailable},
	{"no net namespace", map[string]string{"proc/self/ns/net": ""}, CapNetNamespace, StatusUnavailable},
	{"no nft", map[string]string{"usr/sbin/nft": ""}, CapNetDefaultDeny, StatusUnavailable},
	{"no nf_tables module", map[string]string{"sys/module/nf_tables/refcnt": ""}, CapNetDefaultDeny, StatusUnavailable},
}

// snapshot records every path under root with its mode, size and mtime.
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	got := map[string]string{}
	require.NoError(t, filepath.Walk(root, func(path string, info fs.FileInfo, err error) error {
		if err == nil {
			got[path] = fmt.Sprint(info.Mode(), info.Size(), info.ModTime())
		}
		return err
	}))
	return got
}

func TestHostProberFakeRoots(t *testing.T) {
	root, want := fakeHost(t, nil)
	before := snapshot(t, root)
	res, err := HostProber{Root: root}.Probe(context.Background(), want)
	require.NoError(t, err)
	require.Equal(t, before, snapshot(t, root), "the probe changed the fake host")
	require.Equal(t, "6.8.0-generic linux/"+runtime.GOARCH, res.Host)
	for _, c := range []Capability{CapKernelFloor, CapNamespaces, CapSeccompTrap, CapCgroupV2, CapCgroupControllers, CapCgroupGroupOOM,
		CapCgroupKill, CapWorkspaceQuota, CapNetNamespace, CapNetDefaultDeny, CapRuntimeBinary} {
		require.Equal(t, StatusAvailable, res.Capabilities[c].Status, "%s: %s", c, res.Capabilities[c].Detail)
	}
	for _, tc := range fakeRootCases {
		t.Run(tc.name, func(t *testing.T) {
			root, want := fakeHost(t, tc.override)
			res, err := HostProber{Root: root}.Probe(context.Background(), want)
			require.NoError(t, err)
			require.Equal(t, tc.want, res.Capabilities[tc.cap].Status, res.Capabilities[tc.cap].Detail)
		})
	}
}
