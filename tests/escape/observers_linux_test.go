package escape

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	ranFile    = "ran"  // every case first writes this into the workspace
	inputName  = "data" // the approved input the mutable-input and path-race cases read
	runTimeout = 30 * time.Second
	canaryWait = 200 * time.Millisecond // how long a listener waits for a queued connection or datagram
	lateWait   = 2 * time.Second        // how long to watch for a background job's late write
)

// markerPrefix starts every marker this test process hands out, so one scan finds them all.
var markerPrefix = fmt.Sprintf("escape-%d-", os.Getpid())

func init() { exercise = execute }

// execute runs c once through r and reports what the observer saw.
func execute(t *testing.T, r Runner, tg Targets, c Case) (outcome, error) {
	t.Helper()
	w := newWorld(t, tg)
	if c.Setup != nil {
		c.Setup(t, w)
	}
	check, err := c.Observe(t, w)
	if err != nil {
		return outcome{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()
	script := ": > \"$ESCAPE_WORKSPACE/" + ranFile + "\"\n" + c.Script
	spec := Spec{ID: c.ID(), Argv: []string{"/bin/sh", "-c", script}, Env: w.Env, Workspace: w.Workspace,
		InputDir: w.Inputs, Inputs: w.Digests, AfterCheck: w.AfterCheck, Output: &w.Out}
	if err := r.Run(ctx, spec); err != nil {
		t.Fatalf("runner failed on %s: %v\noutput:\n%s", c.ID(), err, w.Out.String())
	}
	_, statErr := os.Stat(filepath.Join(w.Workspace, ranFile))
	return outcome{ran: statErr == nil, evidence: check(), output: w.Out.String()}, nil
}

func newWorld(t *testing.T, tg Targets) *World {
	t.Helper()
	root := t.TempDir()
	w := &World{Workspace: filepath.Join(root, "workspace"), Inputs: filepath.Join(root, "inputs"),
		Outside: filepath.Join(root, "outside"), Secrets: filepath.Join(root, "secrets"),
		Marker: markerPrefix + randomHex(t), Secret: "cordum-escape-canary-" + randomHex(t),
		Targets: tg, Digests: map[string]string{}, AfterCheck: func() error { return nil }}
	for _, dir := range []string{w.Workspace, w.Inputs, w.Outside, w.Secrets} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	w.Env = []string{"ESCAPE_MARKER=" + w.Marker, "ESCAPE_SELF=" + self, "ESCAPE_OUTSIDE=" + w.Outside}
	t.Cleanup(func() { reap(t, w.Marker) })
	return w
}

func randomHex(t *testing.T) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

// marked lists live processes whose environment holds ESCAPE_MARKER=<prefix...>.
// A zombie has an empty environment, so only running processes count.
func marked(prefix string) []int {
	entries, _ := os.ReadDir("/proc")
	needle := []byte("ESCAPE_MARKER=" + prefix)
	var pids []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		env, err := os.ReadFile(filepath.Join("/proc", e.Name(), "environ"))
		if err != nil {
			continue
		}
		for _, kv := range bytes.Split(env, []byte{0}) {
			if bytes.HasPrefix(kv, needle) {
				pids = append(pids, pid)
				break
			}
		}
	}
	return pids
}

// reap kills every process carrying marker, repeating while a spawn loop may still fork.
func reap(t *testing.T, marker string) {
	for range 50 {
		pids := marked(marker)
		if len(pids) == 0 {
			return
		}
		for _, pid := range pids {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("processes carrying %s survived 50 kill rounds: %v", marker, marked(marker))
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// outsideWrite trips when anything appears in the canary directory outside the workspace.
func outsideWrite(_ *testing.T, w *World) (func() string, error) {
	return func() string {
		entries, _ := os.ReadDir(w.Outside)
		if len(entries) == 0 {
			return ""
		}
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		return fmt.Sprintf("wrote %v in %s, outside the workspace", names, w.Outside)
	}, nil
}

// streamCanary listens on network/addr, hands the bound address to the case in
// envName, and trips when a connection is queued on it.
func streamCanary(network, envName string, addr func(Targets) string) func(*testing.T, *World) (func() string, error) {
	return func(t *testing.T, w *World) (func() string, error) {
		target := addr(w.Targets)
		ln, err := net.Listen(network, target)
		if err != nil {
			return nil, fmt.Errorf("cannot bind the %s canary at %s on this host (an existing socket is never touched): %w", network, target, err)
		}
		t.Cleanup(func() { _ = ln.Close() })
		w.Env = append(w.Env, envName+"="+ln.Addr().String())
		return func() string {
			_ = ln.(interface{ SetDeadline(time.Time) error }).SetDeadline(time.Now().Add(canaryWait))
			conn, err := ln.Accept()
			if err != nil {
				return ""
			}
			_ = conn.Close()
			return fmt.Sprintf("a %s connection reached the canary at %s", network, ln.Addr())
		}, nil
	}
}

func listenAddr(tg Targets) string { return net.JoinHostPort(tg.ListenHost, "0") }

// datagramCanary listens for UDP on the listen host, hands its address to the
// case in envName, and trips on a datagram that contains want.
func datagramCanary(envName, want string) func(*testing.T, *World) (func() string, error) {
	return func(t *testing.T, w *World) (func() string, error) {
		pc, err := net.ListenPacket("udp", listenAddr(w.Targets))
		if err != nil {
			return nil, fmt.Errorf("cannot bind the UDP canary on %s: %w", w.Targets.ListenHost, err)
		}
		t.Cleanup(func() { _ = pc.Close() })
		w.Env = append(w.Env, envName+"="+pc.LocalAddr().String())
		return func() string {
			buf := make([]byte, 2048)
			for {
				_ = pc.SetReadDeadline(time.Now().Add(canaryWait))
				n, from, err := pc.ReadFrom(buf)
				if err != nil {
					return ""
				}
				if bytes.Contains(buf[:n], []byte(want)) {
					return fmt.Sprintf("a datagram from %s carrying %q reached the canary at %s", from, want, pc.LocalAddr())
				}
			}
		}, nil
	}
}

// survivors trips when a process of the case outlives the run, or when a
// background job writes "late" into the workspace after the run ended.
func survivors(_ *testing.T, w *World) (func() string, error) {
	return func() string {
		late := filepath.Join(w.Workspace, "late")
		lateAtReturn := exists(late)
		if pids := marked(w.Marker); len(pids) > 0 {
			return fmt.Sprintf("%d processes carrying the case marker outlived the run: %v", len(pids), pids)
		}
		for deadline := time.Now().Add(lateWait); !lateAtReturn && time.Now().Before(deadline); {
			if exists(late) {
				return "a background job wrote to the workspace after the run ended"
			}
			time.Sleep(50 * time.Millisecond)
		}
		return ""
	}, nil
}

// consumed trips when the bytes the case copied from its input differ from the approved bytes.
func consumed(_ *testing.T, w *World) (func() string, error) {
	want := w.Digests[inputName]
	return func() string {
		got, err := os.ReadFile(filepath.Join(w.Workspace, "consumed"))
		if err != nil || sha256Hex(got) == want {
			return ""
		}
		return fmt.Sprintf("the case consumed bytes with sha256 %s, not the approved %s", sha256Hex(got), want)
	}, nil
}

// spawned trips when the case started ProcCap processes that each marked their start.
func spawned(_ *testing.T, w *World) (func() string, error) {
	limit := w.Targets.ProcCap
	w.Env = append(w.Env, "ESCAPE_PROC_CAP="+strconv.Itoa(limit))
	return func() string {
		started, _ := filepath.Glob(filepath.Join(w.Workspace, "p.*"))
		if len(started) < limit {
			return ""
		}
		return fmt.Sprintf("%d processes ran at once (cap %d)", len(started), limit)
	}, nil
}

// allocated trips when the case reports holding MemMiB of memory.
func allocated(_ *testing.T, w *World) (func() string, error) {
	limit := w.Targets.MemMiB << 20
	w.Env = append(w.Env, "ESCAPE_MEM_BYTES="+strconv.Itoa(limit))
	return func() string {
		b, err := os.ReadFile(filepath.Join(w.Workspace, "alloc"))
		if err != nil {
			return ""
		}
		held, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err != nil || held < limit {
			return ""
		}
		return fmt.Sprintf("the case held %d bytes (cap %d)", held, limit)
	}, nil
}

// leaked trips when the seeded secret appears in the case's output or in any workspace file.
func leaked(_ *testing.T, w *World) (func() string, error) {
	return func() string {
		if strings.Contains(w.Out.String(), w.Secret) {
			return "the seeded secret appeared in the case's output"
		}
		hit := ""
		_ = filepath.WalkDir(w.Workspace, func(path string, d fs.DirEntry, err error) error {
			if err != nil || !d.Type().IsRegular() {
				return nil
			}
			if b, err := os.ReadFile(path); err == nil && bytes.Contains(b, []byte(w.Secret)) {
				hit = path
				return filepath.SkipAll
			}
			return nil
		})
		if hit == "" {
			return ""
		}
		return "the seeded secret appeared in " + hit
	}, nil
}
