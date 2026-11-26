package escape

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// hostRunner runs a case with no confinement, the way a naive executor would:
// it inherits this process's environment, checks no input before calling
// AfterCheck, and leaves background jobs running. The self-tests use it to
// prove that each observer sees its effect.
type hostRunner struct{}

func (hostRunner) Run(ctx context.Context, s Spec) error {
	if err := s.AfterCheck(); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, s.Argv[0], s.Argv[1:]...)
	cmd.Env = append(append(os.Environ(), s.Env...), "ESCAPE_WORKSPACE="+s.Workspace, "ESCAPE_INPUT_DIR="+s.InputDir)
	cmd.Dir = s.Workspace
	cmd.Stdout, cmd.Stderr = s.Output, s.Output
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return nil
	}
	return err
}

// fakeRunner stands in for a runner row and registers the same way. It starts
// no program: it records the case id and writes the start marker that a
// program inside a perfectly confining profile would write.
type fakeRunner struct{ ids []string }

func (f *fakeRunner) Run(_ context.Context, s Spec) error {
	f.ids = append(f.ids, s.ID)
	return os.WriteFile(filepath.Join(s.Workspace, ranFile), nil, 0o600)
}

// loopbackTargets keeps every self-test target on loopback or under t.TempDir(),
// with small bounds, so an unconfined run cannot reach past the test host.
func loopbackTargets(t *testing.T) Targets {
	dir := t.TempDir()
	return Targets{ListenHost: "127.0.0.1", Metadata: "127.0.0.1:0",
		DockerSocket: filepath.Join(dir, "docker.sock"), ContainerdSocket: filepath.Join(dir, "containerd.sock"),
		ProcCap: 16, MemMiB: 32}
}

func runnable(t *testing.T) []Case {
	var out []Case
	for _, c := range corpus {
		if reason := c.missing(); reason != "" {
			t.Logf("%s: %s", c.ID(), reason)
			continue
		}
		out = append(out, c)
	}
	return out
}

func TestObserversTripUnconfined(t *testing.T) {
	tg := loopbackTargets(t)
	for _, c := range runnable(t) {
		t.Run(c.ID(), func(t *testing.T) {
			out, err := execute(t, hostRunner{}, tg, c)
			if err != nil {
				t.Fatal(err)
			}
			if !out.ran || out.evidence == "" {
				t.Fatalf("ran=%v, and the observer saw no effect from the unconfined run\noutput:\n%s", out.ran, out.output)
			}
			t.Logf("observer tripped: %s", out.evidence)
		})
	}
	if pids := marked(markerPrefix); len(pids) > 0 {
		t.Fatalf("marker-tagged processes survived the self-test: %v", pids)
	}
}

func TestRegisteredRunnerGetsEveryCase(t *testing.T) {
	saved := registry
	t.Cleanup(func() { registry = saved })
	registry = nil
	fake := &fakeRunner{}
	Register("fake", fake, loopbackTargets(t))
	t.Run("corpus", runCorpus)
	var want []string
	for _, c := range runnable(t) {
		want = append(want, c.ID())
	}
	if !slices.Equal(fake.ids, want) {
		t.Fatalf("fake runner got %v, want %v", fake.ids, want)
	}
}

func TestEveryClassHasTwoCases(t *testing.T) {
	perClass := map[Class]int{}
	seen := map[string]bool{}
	for _, c := range corpus {
		if seen[c.ID()] {
			t.Errorf("duplicate case id %s", c.ID())
		}
		seen[c.ID()] = true
		if c.Needs == "" {
			perClass[c.Class]++
		}
	}
	for _, class := range classes {
		if perClass[class] < 2 {
			t.Errorf("class %s has %d cases that need nothing beyond sh, want at least 2", class, perClass[class])
		}
	}
	if len(perClass) != len(classes) {
		t.Errorf("cases use %d classes, want the %d declared", len(perClass), len(classes))
	}
}
