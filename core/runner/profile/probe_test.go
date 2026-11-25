package profile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeHost writes a complete fake Linux host under a temp root; an override with empty
// content leaves that file out. The profile's runsc hash matches the unmodified fake binary.
func fakeHost(t *testing.T, override map[string]string) (string, Profile) {
	t.Helper()
	files := map[string]string{
		"proc/sys/kernel/osrelease":               "6.8.0-generic",
		"proc/sys/user/max_user_namespaces":       "63000",
		"proc/sys/kernel/seccomp/actions_avail":   "kill_process kill_thread trap errno user_notif trace log allow",
		"proc/self/mountinfo":                     "30 1 0:26 / /sys/fs/cgroup rw - cgroup2 cgroup2 rw",
		"proc/self/cgroup":                        "0::/runner",
		"proc/filesystems":                        "nodev\tsysfs\nnodev\ttmpfs",
		"sys/fs/cgroup/runner/cgroup.controllers": "cpuset cpu io memory pids",
		"sys/fs/cgroup/runner/memory.oom.group":   "0",
		"sys/fs/cgroup/runner/cgroup.kill":        "0",
		"sys/module/nf_tables/refcnt":             "0",
		"usr/sbin/nft":                            "nft",
		"usr/local/bin/runsc":                     "runsc",
	}
	for _, ns := range []string{"mnt", "pid", "ipc", "net", "user"} {
		files["proc/self/ns/"+ns] = ns
	}
	maps.Copy(files, override)
	root := t.TempDir()
	for rel, content := range files {
		if content != "" {
			path := filepath.Join(root, rel)
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
			require.NoError(t, os.WriteFile(path, []byte(content+"\n"), 0o755))
		}
	}
	sum := sha256.Sum256([]byte("runsc\n"))
	return root, Profile{Runtime: Runtime{BinaryPath: "/usr/local/bin/runsc", BinarySHA256: hex.EncodeToString(sum[:])}}
}

func TestHostProberReportsEveryCapability(t *testing.T) {
	res, err := HostProber{}.Probe(context.Background(), Profile{})
	require.NoError(t, err)
	require.False(t, res.ProbedAt.IsZero())
	require.Contains(t, res.Host, runtime.GOOS+"/"+runtime.GOARCH)
	require.Len(t, res.Capabilities, len(AllCapabilities()))
	for _, c := range AllCapabilities() {
		got, ok := res.Capabilities[c]
		require.True(t, ok, "no status for %s", c)
		require.Contains(t, []Status{StatusAvailable, StatusUnavailable, StatusUnproven}, got.Status, c)
		require.NotEmpty(t, got.Detail, c)
		t.Logf("%s: %s (%s)", c, got.Status, got.Detail)
	}
	require.Equal(t, StatusUnavailable, res.Capabilities[CapRuntimeBinary].Status, "an empty binary path is never available")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = HostProber{}.Probe(ctx, Profile{})
	require.ErrorIs(t, err, context.Canceled)
}

func TestHostProberNotLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("covers non-linux builds")
	}
	root, want := fakeHost(t, nil)
	res, err := HostProber{Root: root}.Probe(context.Background(), want)
	require.NoError(t, err)
	for _, c := range AllCapabilities() {
		require.Equal(t, CapabilityResult{Status: StatusUnavailable, Detail: "not linux (GOOS=" + runtime.GOOS + ")"}, res.Capabilities[c], c)
	}
}

func TestHostProberUnprovenNeverAvailable(t *testing.T) {
	want := StatusUnproven
	if runtime.GOOS != "linux" {
		want = StatusUnavailable
	}
	root, profile := fakeHost(t, nil)
	for _, prober := range []HostProber{{}, {Root: root}} {
		res, err := prober.Probe(context.Background(), profile)
		require.NoError(t, err)
		for _, c := range []Capability{CapSystrap, CapCloneIntoCgroup, CapGuestTaskCeiling, CapGuestSyscallPolicy, CapOutputBudget, CapWorkloadIsolation} {
			require.Equal(t, want, res.Capabilities[c].Status, "%s on root %q", c, prober.Root)
		}
	}
}
