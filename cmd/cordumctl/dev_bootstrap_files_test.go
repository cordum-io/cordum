package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// These tests run `up --dev`'s file bootstrap in helper processes of this test binary, as concurrent cordumctl
// runs would. Production code never reads devBootstrapBarrierEnv: task-bbb80101's verify adds, via -overlay, a
// barrier that records each helper's arrival in that directory and holds it until the test releases it.
const (
	devBootstrapHelperEnv  = "CORDUMCTL_DEV_BOOTSTRAP_HELPER"
	devBootstrapBarrierEnv = "CORDUM_DEV_BOOTSTRAP_TEST_BARRIER"
	devBootstrapLockFile   = ".cordum-dev-bootstrap.lock"
)

var devBootstrapCredentialEnv = []string{"CORDUM_API_KEY", "API_KEY", "REDIS_PASSWORD", "CORDUM_ADMIN_USERNAME",
	"CORDUM_ADMIN_PASSWORD", "CORDUM_MCP_ENABLED", "CORDUM_MCP_POLICY_GATE_ENABLED", "CORDUM_TENANT_ID"}

type devBootstrapChild struct {
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
	APIKey  string `json:"api_key,omitempty"`
	Created bool   `json:"created,omitempty"`
}

// TestDevBootstrapHelperProcess is the helper process the tests below start; it is not a test itself.
func TestDevBootstrapHelperProcess(t *testing.T) {
	mode := os.Getenv(devBootstrapHelperEnv)
	if mode == "" {
		t.Skip("helper process for the dev bootstrap file tests")
	}
	dir := os.Getenv("CORDUMCTL_DEV_BOOTSTRAP_DIR")
	var out devBootstrapChild
	var err error
	if mode == "env" {
		var res devBootstrapResult
		res, err = ensureDevBootstrapEnv(dir)
		out.APIKey = res.APIKey
	} else {
		_, out.Created, err = ensureDevCLIContext(dir, os.Getenv("CORDUMCTL_DEV_BOOTSTRAP_TENANT"))
	}
	if out.OK = err == nil; err != nil {
		out.Error = err.Error()
	}
	_ = json.NewEncoder(os.Stdout).Encode(out) // the test fails on output it cannot decode
	os.Exit(0)
}

// TestDevBootstrapConcurrentResultsMatchDisk: two runs on one .env must not both generate credentials.
// Every run that succeeds returns the credentials the final file holds; a refused run returns none.
func TestDevBootstrapConcurrentResultsMatchDisk(t *testing.T) {
	for _, tc := range []struct {
		name   string
		seeded bool
	}{{name: "empty .env", seeded: true}, {name: "missing .env"}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			envPath := filepath.Join(dir, ".env")
			if tc.seeded {
				writeTestFile(t, envPath, nil)
			}
			results, engaged := runDevBootstrapChildren(t, "env", dir, []string{"a", "b"}, nil)
			final := readEnvFile(envPath, "CORDUM_API_KEY")
			succeeded := 0
			for i, r := range results {
				if !r.OK {
					assertRefusedDevBootstrap(t, r)
					continue
				}
				succeeded++
				if r.APIKey != final {
					t.Errorf("run %d returned CORDUM_API_KEY %q, but .env holds %q", i, r.APIKey, final)
				}
			}
			// With the verify's barrier, one run waits inside the lock until the other has exited, so the
			// fixed code refuses exactly one run; the pre-fix code lets both through to the barrier.
			t.Logf("barrier engaged: %v, refused runs: %d", engaged, len(results)-succeeded)
			if succeeded == 0 {
				t.Errorf("no run succeeded: %+v", results)
			}
			assertNoDevBootstrapLeftovers(t, dir)
		})
	}
}

// TestDevBootstrapLockHeldFailsAndRetryPreserves: a run that finds the bootstrap lock held, even one a dead
// run left, stops with a retry message and changes nothing; after the lock is gone a retry keeps what .env has.
func TestDevBootstrapLockHeldFailsAndRetryPreserves(t *testing.T) {
	for _, tc := range []struct {
		name     string
		existing bool
	}{{name: "existing .env", existing: true}, {name: "missing .env"}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			envPath := filepath.Join(dir, ".env")
			committed := ""
			if tc.existing {
				committed = runDevBootstrapChild(t, "env", dir).APIKey
			}
			before, beforeErr := os.ReadFile(envPath)
			lockPath := filepath.Join(dir, devBootstrapLockFile)
			held := []byte("held by another up --dev\n")
			writeTestFile(t, lockPath, held)
			assertRefusedDevBootstrap(t, runDevBootstrapChild(t, "env", dir))
			after, afterErr := os.ReadFile(envPath)
			if !bytes.Equal(after, before) || os.IsNotExist(afterErr) != os.IsNotExist(beforeErr) {
				t.Errorf(".env changed while the lock was held: %q (%v), was %q (%v)", after, afterErr, before, beforeErr)
			}
			if got, err := os.ReadFile(lockPath); err != nil || !bytes.Equal(got, held) {
				t.Errorf("the held lock was taken over or removed: %q, %v", got, err)
			}
			if err := os.Remove(lockPath); err != nil {
				t.Fatalf("release lock: %v", err)
			}
			retry := runDevBootstrapChild(t, "env", dir)
			if got := readEnvFile(envPath, "CORDUM_API_KEY"); !retry.OK || retry.APIKey != got || got == "" {
				t.Fatalf("retry after release: %+v; .env holds CORDUM_API_KEY %q", retry, got)
			}
			if after, _ := os.ReadFile(envPath); tc.existing && (retry.APIKey != committed || !bytes.Equal(after, before)) {
				t.Errorf("retry returned %q and left .env %q; want the committed %q and .env unchanged", retry.APIKey, after, committed)
			}
			assertNoDevBootstrapLeftovers(t, dir)
		})
	}
}

// TestDevBootstrapEnvReplaceFailureKeepsPriorBytes: a .env that cannot be replaced fails the bootstrap and
// keeps its previous bytes instead of being truncated and rewritten in place. Windows refuses to replace a
// file another handle holds open, which makes the failure real; POSIX rename never refuses, so Windows only.
func TestDevBootstrapEnvReplaceFailureKeepsPriorBytes(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only Windows refuses to replace a file that another handle holds open")
	}
	for _, key := range devBootstrapCredentialEnv {
		t.Setenv(key, "")
	}
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	seed := []byte("REDIS_PASSWORD=existing-redis\n")
	writeTestFile(t, envPath, seed)
	held, err := os.Open(envPath)
	if err != nil {
		t.Fatalf("open .env: %v", err)
	}
	res, err := ensureDevBootstrapEnv(dir)
	_ = held.Close()
	if err == nil || res.APIKey != "" {
		t.Errorf("bootstrap while .env is held open: api key %q, error %v; want an error and no credentials", res.APIKey, err)
	}
	if got, _ := os.ReadFile(envPath); !bytes.Equal(got, seed) {
		t.Errorf(".env = %q after the failed bootstrap, want the previous %q", got, seed)
	}
	assertNoDevBootstrapLeftovers(t, dir)

	res, err = ensureDevBootstrapEnv(dir)
	if after, _ := os.ReadFile(envPath); err != nil || readEnvFile(envPath, "CORDUM_API_KEY") != res.APIKey || readEnvFile(envPath, "REDIS_PASSWORD") != "existing-redis" {
		t.Errorf("retry: api key %q, error %v; want it in .env beside the previous REDIS_PASSWORD: %q", res.APIKey, err, after)
	}
}

// TestDevCLIContextLateWinnerPreserved: a cordum.yaml that appears after ensureDevCLIContext found none keeps
// its bytes, at most one run reports creating it, and a failed publication leaves nothing behind.
func TestDevCLIContextLateWinnerPreserved(t *testing.T) {
	t.Run("late competitor", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "cordum.yaml")
		competitor := []byte("gateway: https://competitor.example.com\n")
		results, engaged := runDevBootstrapChildren(t, "yaml", dir, []string{"tenant-a"}, func() {
			writeTestFile(t, cfgPath, competitor)
		})
		t.Logf("barrier engaged: %v", engaged)
		got, _ := os.ReadFile(cfgPath)
		switch {
		case !engaged: // plain build: nothing can appear between the check and the publication
			assertOneDevCLIContextCreator(t, dir, results, []string{"tenant-a"})
		case !results[0].OK || results[0].Created || !bytes.Equal(got, competitor):
			t.Errorf("after a competing cordum.yaml appeared: %+v, file %q; want created=false and %q", results[0], got, competitor)
		}
		assertNoDevBootstrapLeftovers(t, dir)
	})
	t.Run("concurrent creators", func(t *testing.T) {
		dir := t.TempDir()
		tenants := []string{"tenant-a", "tenant-b"}
		results, engaged := runDevBootstrapChildren(t, "yaml", dir, tenants, nil)
		t.Logf("barrier engaged: %v", engaged)
		assertOneDevCLIContextCreator(t, dir, results, tenants)
		assertNoDevBootstrapLeftovers(t, dir)
	})
	t.Run("publication error", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "missing")
		if _, created, err := ensureDevCLIContext(dir, "tenant-a"); err == nil || created {
			t.Errorf("ensureDevCLIContext in a missing directory: created=%v, error %v; want an error", created, err)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("a failed publication created %s: %v", dir, err)
		}
	})
}

func assertOneDevCLIContextCreator(t *testing.T, dir string, results []devBootstrapChild, tenants []string) {
	t.Helper()
	var creators []string
	for i, r := range results {
		if !r.OK {
			t.Errorf("run %d failed: %s", i, r.Error)
		} else if r.Created {
			creators = append(creators, tenants[i])
		}
	}
	if len(creators) != 1 {
		t.Fatalf("%d runs reported creating cordum.yaml (%v), want exactly 1", len(creators), creators)
	}
	cfg, _, err := LoadEdgeClaudeConfig([]string{"CORDUM_API_KEY=secret-from-env"}, dir, t.TempDir())
	if err != nil || cfg.Tenant != creators[0] {
		t.Errorf("cordum.yaml has tenant %q (error %v), want the creator's %q", cfg.Tenant, err, creators[0])
	}
}

func assertRefusedDevBootstrap(t *testing.T, r devBootstrapChild) {
	t.Helper()
	if r.OK || r.APIKey != "" || !strings.Contains(r.Error, "retry") {
		t.Errorf("refused run: ok=%v api key %q error %q; want a retry error and no credentials", r.OK, r.APIKey, r.Error)
	}
}

// assertNoDevBootstrapLeftovers fails on a bootstrap lock or a staging file left in dir.
func assertNoDevBootstrapLeftovers(t *testing.T, dir string) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if n := e.Name(); n == devBootstrapLockFile || strings.HasPrefix(n, ".managed-settings-") || strings.HasPrefix(n, ".cordum-yaml-stage-") {
			t.Errorf("bootstrap left %s in %s", n, dir)
		}
	}
}

func runDevBootstrapChild(t *testing.T, mode, dir string) devBootstrapChild {
	t.Helper()
	results, _ := runDevBootstrapChildren(t, mode, dir, []string{""}, nil)
	return results[0]
}

// runDevBootstrapChildren starts one helper per tenant, all in mode on dir, and returns their results. When
// every helper has reached the overlay barrier, beforeRelease runs and the barrier is released; when a helper
// exits first (the plain build has no barrier, and a run refused at the lock never reaches it), it is
// released then. engaged reports whether all helpers met at the barrier.
func runDevBootstrapChildren(t *testing.T, mode, dir string, tenants []string, beforeRelease func()) ([]devBootstrapChild, bool) {
	t.Helper()
	barrier := t.TempDir()
	point := map[string]string{"env": "env-read", "yaml": "yaml-stat"}[mode]
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmds := make([]*exec.Cmd, len(tenants))
	stdout, stderr := make([]bytes.Buffer, len(tenants)), make([]bytes.Buffer, len(tenants))
	exited := make(chan struct{}, len(tenants))
	for i, tenant := range tenants {
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDevBootstrapHelperProcess$")
		cmd.Env = devBootstrapChildEnv(mode, dir, barrier, tenant)
		cmd.Stdout, cmd.Stderr = &stdout[i], &stderr[i]
		if err := cmd.Start(); err != nil {
			t.Fatalf("start helper: %v", err)
		}
		cmds[i] = cmd
		go func() { _ = cmd.Wait(); exited <- struct{}{} }()
	}
	done, engaged := 0, false
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for !engaged && done == 0 {
		select {
		case <-exited:
			done++
		case <-tick.C:
			arrived, _ := filepath.Glob(filepath.Join(barrier, point+".*.arrived"))
			engaged = len(arrived) == len(tenants)
		}
	}
	if engaged && beforeRelease != nil {
		beforeRelease()
	}
	writeTestFile(t, filepath.Join(barrier, point+".release"), nil)
	for ; done < len(tenants); done++ {
		<-exited
	}
	results := make([]devBootstrapChild, len(tenants))
	for i, cmd := range cmds {
		if err := json.Unmarshal(stdout[i].Bytes(), &results[i]); err != nil || cmd.ProcessState.ExitCode() != 0 {
			t.Fatalf("helper %d: %v, decode %v\nstdout: %s\nstderr: %s", i, cmd.ProcessState, err, &stdout[i], &stderr[i])
		}
	}
	return results, engaged
}

// devBootstrapChildEnv is this process's environment without the credentials ensureDevBootstrapEnv reads.
func devBootstrapChildEnv(mode, dir, barrier, tenant string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if key, _, _ := strings.Cut(kv, "="); !slices.Contains(devBootstrapCredentialEnv, strings.ToUpper(key)) {
			env = append(env, kv)
		}
	}
	return append(env, devBootstrapHelperEnv+"="+mode, "CORDUMCTL_DEV_BOOTSTRAP_DIR="+dir,
		devBootstrapBarrierEnv+"="+barrier, "CORDUMCTL_DEV_BOOTSTRAP_TENANT="+tenant)
}

func writeTestFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
