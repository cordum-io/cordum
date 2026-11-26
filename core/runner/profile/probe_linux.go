//go:build linux

package profile

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// Probe gives every capability a status. Besides reading files under Root, its only syscalls
// are prctl(PR_GET_NO_NEW_PRIVS) and an O_PATH openat2 beneath Root. It fails only on a done ctx.
func (h HostProber) Probe(ctx context.Context, want Profile) (ProbeResult, error) {
	if err := ctx.Err(); err != nil {
		return ProbeResult{}, err
	}
	root := cmp.Or(h.Root, "/")
	in := func(rel string) string { return filepath.Join(root, rel) }
	release := readTrim(in("proc/sys/kernel/osrelease"))
	cg, cgReason := ownCgroup(in)
	cgroupFile := func(name string, words ...string) CapabilityResult {
		if cgReason != "" {
			return CapabilityResult{StatusUnavailable, cgReason}
		}
		if words == nil {
			return exists(filepath.Join(cg, name))
		}
		return hasWords(filepath.Join(cg, name), words...)
	}
	res := ProbeResult{ProbedAt: time.Now().UTC(), Host: strings.TrimSpace(release + " " + runtime.GOOS + "/" + runtime.GOARCH)}
	res.Capabilities = map[Capability]CapabilityResult{
		CapKernelFloor:          kernelFloor(release),
		CapNamespaces:           namespaces(in),
		CapSeccompTrap:          hasWords(in("proc/sys/kernel/seccomp/actions_avail"), "trap"),
		CapNoNewPrivs:           noNewPrivs(),
		CapCgroupV2:             cgroupV2(in("proc/self/mountinfo")),
		CapCgroupControllers:    cgroupFile("cgroup.controllers", "cpu", "memory", "pids"),
		CapCgroupGroupOOM:       cgroupFile("memory.oom.group"),
		CapCgroupKill:           cgroupFile("cgroup.kill"),
		CapInputMaterialization: openBeneath(root),
		CapWorkspaceQuota:       hasWords(in("proc/filesystems"), "tmpfs"),
		CapNetNamespace:         exists(in("proc/self/ns/net")),
		CapNetDefaultDeny:       nftables(in),
		CapRuntimeBinary:        runscBinary(in, want.Runtime),
	}
	// No read-only check can prove these; later rows may change one only with evidence.
	for _, c := range []Capability{CapSystrap, CapCloneIntoCgroup, CapGuestTaskCeiling, CapGuestSyscallPolicy, CapOutputBudget, CapWorkloadIsolation} {
		res.Capabilities[c] = CapabilityResult{StatusUnproven, "no read-only check can prove this"}
	}
	return res, nil
}

func readTrim(path string) string {
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func exists(path string) CapabilityResult {
	if _, err := os.Lstat(path); err != nil {
		return CapabilityResult{StatusUnavailable, path + " missing"}
	}
	return CapabilityResult{StatusAvailable, path + " exists"}
}

func hasWords(path string, words ...string) CapabilityResult {
	got := strings.Fields(readTrim(path))
	if missing := slices.DeleteFunc(slices.Clone(words), func(w string) bool { return slices.Contains(got, w) }); len(missing) > 0 {
		return CapabilityResult{StatusUnavailable, fmt.Sprintf("%s lacks %s (read %q)", path, strings.Join(missing, " "), strings.Join(got, " "))}
	}
	return CapabilityResult{StatusAvailable, path + " has " + strings.Join(words, " ")}
}

func kernelFloor(release string) CapabilityResult {
	var major, minor int
	if _, err := fmt.Sscanf(release, "%d.%d", &major, &minor); err != nil || major < 6 || (major == 6 && minor < 8) {
		return CapabilityResult{StatusUnavailable, fmt.Sprintf("osrelease %q is unreadable or below 6.8", release)}
	}
	return CapabilityResult{StatusAvailable, "osrelease " + release + " is at least 6.8"}
}

func namespaces(in func(string) string) CapabilityResult {
	limit := readTrim(in("proc/sys/user/max_user_namespaces"))
	if n, err := strconv.Atoi(limit); err != nil || n <= 0 {
		return CapabilityResult{StatusUnavailable, fmt.Sprintf("max_user_namespaces=%q", limit)}
	}
	for _, ns := range []string{"mnt", "pid", "ipc", "net", "user"} {
		if r := exists(in("proc/self/ns/" + ns)); r.Status != StatusAvailable {
			return r
		}
	}
	if readTrim(in("proc/sys/kernel/apparmor_restrict_unprivileged_userns")) == "1" {
		return CapabilityResult{StatusUnproven, "apparmor_restrict_unprivileged_userns=1; an LSM may still allow the service"}
	}
	return CapabilityResult{StatusAvailable, "max_user_namespaces=" + limit + "; mnt pid ipc net user namespaces present"}
}

func noNewPrivs() CapabilityResult {
	v, err := unix.PrctlRetInt(unix.PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0)
	if err != nil {
		return CapabilityResult{StatusUnavailable, "prctl(PR_GET_NO_NEW_PRIVS): " + err.Error()}
	}
	return CapabilityResult{StatusAvailable, fmt.Sprintf("prctl(PR_GET_NO_NEW_PRIVS)=%d", v)}
}

// cgroupV2 needs cgroup2 at /sys/fs/cgroup and no v1 mount, so a hybrid fails.
func cgroupV2(mountinfo string) CapabilityResult {
	unified, v1 := false, 0
	for _, line := range strings.Split(readTrim(mountinfo), "\n") {
		pre, post, _ := strings.Cut(line, " - ")
		if mount, fstype := strings.Fields(pre), strings.Fields(post); len(mount) >= 5 && len(fstype) > 0 {
			unified = unified || (fstype[0] == "cgroup2" && mount[4] == "/sys/fs/cgroup")
			if fstype[0] == "cgroup" {
				v1++
			}
		}
	}
	detail := fmt.Sprintf("cgroup2 at /sys/fs/cgroup: %t; cgroup v1 mounts: %d", unified, v1)
	if !unified || v1 > 0 {
		return CapabilityResult{StatusUnavailable, detail}
	}
	return CapabilityResult{StatusAvailable, detail}
}

// ownCgroup returns the prober's own cgroup directory, or why it has none.
func ownCgroup(in func(string) string) (dir, reason string) {
	for _, line := range strings.Split(readTrim(in("proc/self/cgroup")), "\n") {
		if p, ok := strings.CutPrefix(line, "0::"); ok {
			if strings.Contains(p, "..") {
				return "", fmt.Sprintf("own cgroup %q is outside this cgroup namespace", p)
			}
			return in("sys/fs/cgroup/" + p), ""
		}
	}
	return "", "no cgroup v2 entry in /proc/self/cgroup"
}

func openBeneath(root string) CapabilityResult {
	dir, err := unix.Open(root, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return CapabilityResult{StatusUnavailable, "open " + root + ": " + err.Error()}
	}
	how := unix.OpenHow{Flags: unix.O_PATH | unix.O_DIRECTORY | unix.O_CLOEXEC, Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS}
	fd, err := unix.Openat2(dir, ".", &how)
	_ = unix.Close(dir)
	if err != nil {
		return CapabilityResult{StatusUnavailable, "openat2 RESOLVE_BENEATH|RESOLVE_NO_SYMLINKS: " + err.Error()}
	}
	_ = unix.Close(fd)
	return CapabilityResult{StatusAvailable, "openat2 RESOLVE_BENEATH|RESOLVE_NO_SYMLINKS succeeded"}
}

func nftables(in func(string) string) CapabilityResult {
	if r := exists(in("sys/module/nf_tables")); r.Status != StatusAvailable {
		return r
	}
	for _, nft := range []string{in("usr/sbin/nft"), in("sbin/nft"), in("usr/bin/nft")} {
		if isExecutable(nft) {
			return CapabilityResult{StatusAvailable, nft + " and the nf_tables module present"}
		}
	}
	return CapabilityResult{StatusUnavailable, "no executable nft in /usr/sbin, /sbin or /usr/bin"}
}

func isExecutable(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0
}

func runscBinary(in func(string) string, rt Runtime) CapabilityResult {
	path := in(rt.BinaryPath)
	if rt.BinaryPath == "" || !isExecutable(path) {
		return CapabilityResult{StatusUnavailable, fmt.Sprintf("runtime binary %q is not a regular executable file", rt.BinaryPath)}
	}
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return CapabilityResult{StatusUnavailable, err.Error()}
	}
	defer func() { _ = f.Close() }()
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return CapabilityResult{StatusUnavailable, "hash " + path + ": " + err.Error()}
	}
	if got := hex.EncodeToString(sum.Sum(nil)); got != rt.BinarySHA256 {
		return CapabilityResult{StatusUnavailable, fmt.Sprintf("%s sha256 %s, want %q", path, got, rt.BinarySHA256)}
	}
	return CapabilityResult{StatusAvailable, path + " sha256 matches " + rt.BinarySHA256}
}
