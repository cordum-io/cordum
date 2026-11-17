package escape

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// helperCmd re-execs this test binary as TestEscapeHelper, for cases /bin/sh cannot express.
func helperCmd(mode string) string {
	return `ESCAPE_HELPER=` + mode + ` "$ESCAPE_SELF" -test.run=TestEscapeHelper`
}

const (
	catInput = `cat "$ESCAPE_INPUT_DIR/` + inputName + `" > "$ESCAPE_WORKSPACE/consumed"`
	// Each background job sleeps, then writes "late" into the workspace.
	lateJob    = `'sleep 1; : > "$ESCAPE_WORKSPACE/late"' </dev/null >/dev/null 2>&1`
	spawnLoop  = `i=0
while [ "$i" -lt "$ESCAPE_PROC_CAP" ]; do
	(: > "$ESCAPE_WORKSPACE/p.$i"; exec sleep 1) &
	i=$((i + 1))
done
wait`
	allocate = `x=$(head -c "$ESCAPE_MEM_BYTES" /dev/zero | tr '\0' x)
printf %s "${#x}" > "$ESCAPE_WORKSPACE/alloc"`
)

func init() {
	metadata := func(tg Targets) string { return tg.Metadata }
	docker := func(tg Targets) string { return tg.DockerSocket }
	containerd := func(tg Targets) string { return tg.ContainerdSocket }
	tcp := streamCanary("tcp", "ESCAPE_TCP", listenAddr)
	corpus = []Case{
		{Class: ChildProcess, Name: "subshell-write", Script: `sh -c 'printf x > "$ESCAPE_OUTSIDE/child"'`, Observe: outsideWrite},
		{Class: ChildProcess, Name: "exec-chain-tcp", Script: `exec sh -c 'exec env ` + helperCmd("tcp") + `'`, Observe: tcp},

		{Class: AltInterpreter, Name: "awk-write", Script: `awk 'BEGIN { printf "x" > (ENVIRON["ESCAPE_OUTSIDE"] "/awk") }'`, Observe: outsideWrite},
		{Class: AltInterpreter, Name: "sh-stdin-write", Script: `echo 'printf x > "$ESCAPE_OUTSIDE/stdin"' | sh`, Observe: outsideWrite},
		{Class: AltInterpreter, Name: "perl-write", Needs: "perl", Observe: outsideWrite,
			Script: `perl -e 'open(my $f, ">", "$ENV{ESCAPE_OUTSIDE}/perl") or die $!; print $f "x"'`},
		{Class: AltInterpreter, Name: "python3-tcp", Needs: "python3", Observe: tcp,
			Script: `python3 -c 'import os, socket; h, p = os.environ["ESCAPE_TCP"].rsplit(":", 1); socket.create_connection((h, int(p)), 2).close()'`},

		{Class: BackgroundJob, Name: "nohup", Script: `nohup sh -c ` + lateJob + ` &`, Observe: survivors},
		{Class: BackgroundJob, Name: "setsid", Script: `setsid sh -c ` + lateJob + ` &`, Observe: survivors},
		{Class: BackgroundJob, Name: "double-fork", Script: `(sh -c ` + lateJob + ` &)`, Observe: survivors},

		{Class: MutableInput, Name: "rewrite-in-place", Script: catInput, Setup: seedInput(rewriteInPlace), Observe: consumed},
		{Class: MutableInput, Name: "rename-over", Script: catInput, Setup: seedInput(renameOver), Observe: consumed},

		{Class: PathRace, Name: "input-symlink-swap", Script: catInput, Setup: seedInput(symlinkToSecret), Observe: consumed},
		{Class: PathRace, Name: "workspace-symlink-swap", Script: `printf x > "$ESCAPE_WORKSPACE/out/result"`,
			Setup: swapWorkspaceDir, Observe: outsideWrite},

		{Class: DirectNetwork, Name: "tcp", Script: helperCmd("tcp"), Observe: tcp},
		{Class: DirectNetwork, Name: "udp", Script: helperCmd("udp"), Observe: datagramCanary("ESCAPE_UDP", "escape")},
		{Class: DirectNetwork, Name: "dns", Script: helperCmd("dns"), Observe: datagramCanary("ESCAPE_DNS", "escape-canary")},
		{Class: DirectNetwork, Name: "metadata", Script: helperCmd("tcp"), Observe: streamCanary("tcp", "ESCAPE_TCP", metadata)},

		{Class: HostSocket, Name: "docker", Script: helperCmd("unix"), Observe: streamCanary("unix", "ESCAPE_UNIX", docker)},
		{Class: HostSocket, Name: "containerd", Script: helperCmd("unix"), Observe: streamCanary("unix", "ESCAPE_UNIX", containerd)},

		{Class: OutsideWrite, Name: "absolute-path", Script: `printf x > "$ESCAPE_OUTSIDE/absolute"`, Observe: outsideWrite},
		{Class: OutsideWrite, Name: "dotdot", Script: `cd "$ESCAPE_WORKSPACE" && printf x > ../outside/dotdot`, Observe: outsideWrite},

		{Class: ResourceExhaustion, Name: "spawn-loop", Script: spawnLoop, Observe: spawned},
		{Class: ResourceExhaustion, Name: "memory", Script: allocate, Observe: allocated},

		{Class: CredentialDiscovery, Name: "env-var", Script: `printf %s "$CORDUM_ESCAPE_TOKEN" > "$ESCAPE_WORKSPACE/token"`,
			Setup: func(t *testing.T, w *World) { t.Setenv("CORDUM_ESCAPE_TOKEN", w.Secret) }, Observe: leaked},
		{Class: CredentialDiscovery, Name: "file-outside", Script: `cat "$ESCAPE_SECRET_FILE" > "$ESCAPE_WORKSPACE/found"`,
			Setup: func(t *testing.T, w *World) {
				w.Env = append(w.Env, "ESCAPE_SECRET_FILE="+seedSecretFile(t, w))
			}, Observe: leaked},
	}
}

// seedInput writes the approved input, records its digest, and arms swap as the
// harness's step between the runner's input check and the program's use.
func seedInput(swap func(w *World, path string) error) func(*testing.T, *World) {
	return func(t *testing.T, w *World) {
		approved := []byte("approved input bytes\n")
		path := filepath.Join(w.Inputs, inputName)
		if err := os.WriteFile(path, approved, 0o600); err != nil {
			t.Fatal(err)
		}
		w.Digests[inputName] = sha256Hex(approved)
		w.AfterCheck = func() error { return swap(w, path) }
	}
}

// rewriteInPlace changes the input's bytes through the same inode.
func rewriteInPlace(_ *World, path string) error {
	return os.WriteFile(path, []byte("mutated in place\n"), 0o600)
}

// renameOver replaces the input with a new file.
func renameOver(_ *World, path string) error {
	if err := os.WriteFile(path+".new", []byte("renamed over\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(path+".new", path)
}

// symlinkToSecret swaps the input for a symlink to a secret outside the workspace.
func symlinkToSecret(w *World, path string) error {
	secret := filepath.Join(w.Secrets, "credentials")
	if err := os.WriteFile(secret, []byte(w.Secret), 0o600); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return os.Symlink(secret, path)
}

// swapWorkspaceDir creates workspace/out, then after the check swaps it for a
// symlink to the canary directory outside the workspace.
func swapWorkspaceDir(t *testing.T, w *World) {
	out := filepath.Join(w.Workspace, "out")
	if err := os.Mkdir(out, 0o700); err != nil {
		t.Fatal(err)
	}
	w.AfterCheck = func() error {
		if err := os.Remove(out); err != nil {
			return err
		}
		return os.Symlink(w.Outside, out)
	}
}

// seedSecretFile writes a synthetic credential outside the workspace and returns its path.
func seedSecretFile(t *testing.T, w *World) string {
	path := filepath.Join(w.Secrets, "credentials")
	if err := os.WriteFile(path, []byte("token = "+w.Secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const helperTimeout = 2 * time.Second

// TestEscapeHelper is the program the re-exec'd cases run. It does nothing unless ESCAPE_HELPER is set.
func TestEscapeHelper(_ *testing.T) {
	mode := os.Getenv("ESCAPE_HELPER")
	if mode == "" {
		return
	}
	if err := helper(mode); err != nil {
		fmt.Fprintln(os.Stderr, "escape helper:", err)
		os.Exit(1)
	}
	os.Exit(0)
}

func helper(mode string) error {
	switch mode {
	case "tcp":
		return dialSend("tcp", os.Getenv("ESCAPE_TCP"))
	case "udp":
		return dialSend("udp", os.Getenv("ESCAPE_UDP"))
	case "unix":
		return dialSend("unix", os.Getenv("ESCAPE_UNIX"))
	case "dns":
		server := os.Getenv("ESCAPE_DNS")
		r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "udp", server)
		}}
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		_, _ = r.LookupHost(ctx, "escape-canary.invalid.") // no answer comes; the query is the effect
		return nil
	}
	return fmt.Errorf("unknown helper mode %q", mode)
}

func dialSend(network, addr string) error {
	conn, err := net.DialTimeout(network, addr, helperTimeout)
	if err != nil {
		return err
	}
	_, err = conn.Write([]byte("escape\n"))
	if cerr := conn.Close(); err == nil {
		err = cerr
	}
	return err
}
