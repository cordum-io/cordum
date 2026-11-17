// Package escape is the escape corpus for the M3 Linux code runner. Each case
// tries one way out of a declared execution profile, and an observer outside the
// profile checks whether the effect happened. No runner exists yet, so every
// corpus case skips until a runner row registers one. Coverage classes come from
// docs/adr/012-runner-isolation-stack.md ("Escape coverage", X1-X6) plus four
// more named by the M3-02 row.
package escape

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"testing"
)

// Class is an escape class. The first six are ADR-012's X1-X6.
type Class string

const (
	ChildProcess        Class = "child-process"   // X1
	AltInterpreter      Class = "alt-interpreter" // X2
	BackgroundJob       Class = "background-job"  // X3
	MutableInput        Class = "mutable-input"   // X4
	PathRace            Class = "path-race"       // X5
	DirectNetwork       Class = "direct-network"  // X6
	HostSocket          Class = "host-socket"
	OutsideWrite        Class = "outside-write"
	ResourceExhaustion  Class = "resource-exhaustion"
	CredentialDiscovery Class = "credential-discovery"
)

var classes = []Class{ChildProcess, AltInterpreter, BackgroundJob, MutableInput, PathRace,
	DirectNetwork, HostSocket, OutsideWrite, ResourceExhaustion, CredentialDiscovery}

const noRunnerReason = "no escape runner registered; the M3 runner rows register one"

// Spec is one run request. A runner starts Argv inside the profile under test
// with Env as the program's whole environment, adding only ESCAPE_WORKSPACE and
// ESCAPE_INPUT_DIR (the in-profile paths of Workspace and InputDir) and a PATH.
// Argv uses /bin/sh, and some cases exec the test binary named by ESCAPE_SELF,
// so the runner exposes that file read-only at the same path.
type Spec struct {
	ID         string            // case id, "<class>/<name>"
	Argv       []string          // program and arguments
	Env        []string          // KEY=VALUE pairs; nothing else may be inherited
	Workspace  string            // host dir: the only place the program may write
	InputDir   string            // host dir of approved read-only inputs
	Inputs     map[string]string // input file name -> approved sha256 hex
	AfterCheck func() error      // never nil; call once after checking inputs, before the program starts
	Output     io.Writer         // the program's stdout and stderr
}

// Runner runs a Spec inside the profile under test.
type Runner interface {
	// Run returns once the program and every process it started have ended and
	// teardown is complete. The program's exit status is not an error; an error
	// means the runner itself failed, and the case fails.
	Run(ctx context.Context, spec Spec) error
}

// Targets are the host-side addresses and bounds the cases aim at. A zero field
// takes its default. An observer that cannot bind its target on the host makes
// the case skip as not measured, so no case reaches beyond the test host.
type Targets struct {
	// ListenHost is where the TCP, UDP and DNS canaries listen; default 127.0.0.1.
	// A runner whose sandbox has its own network stack must set an address the
	// sandbox would reach if unconfined, or those cases prove nothing.
	ListenHost       string
	Metadata         string // cloud metadata host:port; default 169.254.169.254:80
	DockerSocket     string // default /var/run/docker.sock
	ContainerdSocket string // default /run/containerd/containerd.sock
	ProcCap          int    // processes the spawn case tries to hold at once; default 256
	MemMiB           int    // MiB the allocation case tries to hold; default 512
}

func (tg Targets) withDefaults() Targets {
	def := func(s *string, v string) {
		if *s == "" {
			*s = v
		}
	}
	def(&tg.ListenHost, "127.0.0.1")
	def(&tg.Metadata, "169.254.169.254:80")
	def(&tg.DockerSocket, "/var/run/docker.sock")
	def(&tg.ContainerdSocket, "/run/containerd/containerd.sock")
	if tg.ProcCap == 0 {
		tg.ProcCap = 256
	}
	if tg.MemMiB == 0 {
		tg.MemMiB = 512
	}
	return tg
}

type registration struct {
	name    string
	runner  Runner
	targets Targets
}

var registry []registration

// Register adds a runner to the corpus. A runner row calls it from an init func
// in its own _test.go file in this package, so no case file changes.
func Register(name string, r Runner, tg Targets) {
	registry = append(registry, registration{name: name, runner: r, targets: tg.withDefaults()})
}

// World is one case's host-side layout, all under the case's t.TempDir().
type World struct {
	Workspace, Inputs, Outside, Secrets string
	Marker     string            // ESCAPE_MARKER value every process of the case inherits
	Secret     string            // synthetic credential the case must not be able to emit
	Targets    Targets
	Env        []string          // ESCAPE_* variables handed to the case
	Digests    map[string]string // approved input name -> sha256 hex
	AfterCheck func() error      // the harness's swap between the runner's check and use
	Out        bytes.Buffer      // what the case wrote to stdout and stderr
}

// Case is one escape attempt. Its ID, "<class>/<name>", is stable so later rows can cite it.
type Case struct {
	Class  Class
	Name   string
	Script string // /bin/sh program
	Needs  string // command the case needs on the host's PATH, or ""
	// Setup seeds inputs and secrets and may set w.AfterCheck; it may be nil.
	Setup func(t *testing.T, w *World)
	// Observe arms the canary. The returned func reports evidence of the effect,
	// or "" for none. An error means this host cannot observe the target.
	Observe func(t *testing.T, w *World) (func() string, error)
}

func (c Case) ID() string { return string(c.Class) + "/" + c.Name }

func (c Case) missing() string {
	if c.Needs == "" {
		return ""
	}
	if _, err := exec.LookPath(c.Needs); err != nil {
		return fmt.Sprintf("%s is not installed on this host; the case runs where it is", c.Needs)
	}
	return ""
}

type outcome struct {
	ran      bool   // the program wrote its start marker into the workspace
	evidence string // what the observer saw; "" means no effect
	output   string
}

// The Linux files set these. Off Linux they stay empty and the corpus skips.
var (
	corpus   []Case
	exercise func(t *testing.T, r Runner, tg Targets, c Case) (outcome, error)
)

func TestCorpus(t *testing.T) { runCorpus(t) }

func runCorpus(t *testing.T) {
	if exercise == nil {
		t.Skipf("the escape corpus needs Linux (/proc, Unix sockets, POSIX job control); this is %s", runtime.GOOS)
	}
	for _, c := range corpus {
		t.Run(c.ID(), func(t *testing.T) {
			if len(registry) == 0 {
				t.Skip(noRunnerReason)
			}
			if reason := c.missing(); reason != "" {
				t.Skip(reason)
			}
			for _, reg := range registry {
				t.Run(reg.name, func(t *testing.T) {
					out, err := exercise(t, reg.runner, reg.targets, c)
					switch {
					case err != nil:
						t.Skipf("not measured: %v", err)
					case !out.ran:
						t.Fatalf("%s started no program; a run that starts nothing proves nothing\noutput:\n%s", reg.name, out.output)
					case out.evidence != "":
						t.Fatalf("escaped %s: %s\noutput:\n%s", reg.name, out.evidence, out.output)
					}
				})
			}
		})
	}
}
