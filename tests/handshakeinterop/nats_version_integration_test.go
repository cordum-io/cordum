//go:build handshakeinterop

package handshakeinterop

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"runtime/debug"
	"strings"
	"testing"

	gnats "github.com/nats-io/nats-server/v2/server"
)

const natsServerModulePath = "github.com/nats-io/nats-server/v2"

var (
	errNATSBuildInfoMissing = errors.New("Go build information is unavailable")
	errNATSModuleMissing    = errors.New("NATS server module is not in the build information")
	errNATSModuleDuplicated = errors.New("NATS server module appears more than once in the build information")
	errNATSModuleReplaced   = errors.New("NATS server module is replaced by a different module")
	errNATSVersionMalformed = errors.New("NATS server module version is not a clean v2 release")
	errNATSVersionMismatch  = errors.New("embedded NATS runtime version does not match the built dependency")

	// Stdlib equivalent of golang.org/x/mod/module's pseudo-version pattern;
	// x/mod is deliberately not a dependency of this module.
	natsPseudoVersion = regexp.MustCompile(`^v[0-9]+\.(0\.0-|[0-9]+\.[0-9]+-([^+]*\.)?0\.)[0-9]{14}-[A-Za-z0-9]+(\+[0-9A-Za-z.-]+)?$`)
	natsCleanVersion  = regexp.MustCompile(`^v(2\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?)$`)
)

// builtNATSServerVersion returns the NATS server version (without the leading
// "v") that the Go toolchain linked into this binary. It fails closed unless
// exactly one clean release of the module, or a same-module versioned replace,
// is recorded.
func builtNATSServerVersion(info *debug.BuildInfo, ok bool) (string, error) {
	if !ok || info == nil {
		return "", errNATSBuildInfoMissing
	}
	var dep *debug.Module
	for _, candidate := range info.Deps {
		if candidate == nil || candidate.Path != natsServerModulePath {
			continue
		}
		if dep != nil {
			return "", fmt.Errorf("%w: %s", errNATSModuleDuplicated, natsServerModulePath)
		}
		dep = candidate
	}
	if dep == nil {
		return "", fmt.Errorf("%w: %s", errNATSModuleMissing, natsServerModulePath)
	}
	version := dep.Version
	if dep.Replace != nil {
		if dep.Replace.Path != natsServerModulePath {
			return "", fmt.Errorf("%w: %s => %s", errNATSModuleReplaced, natsServerModulePath, dep.Replace.Path)
		}
		// A same-path replace leaves dep.Version at the required version while
		// the binary embeds the replacement's version.
		version = dep.Replace.Version
	}
	if natsPseudoVersion.MatchString(version) {
		return "", fmt.Errorf("%w: %q is a pseudo-version", errNATSVersionMalformed, version)
	}
	match := natsCleanVersion.FindStringSubmatch(version)
	if match == nil {
		return "", fmt.Errorf("%w: %q", errNATSVersionMalformed, version)
	}
	return match[1], nil
}

// checkEmbeddedNATSVersion verifies the embedded server's runtime version
// (gnats.VERSION) equals the module version built into the test binary.
func checkEmbeddedNATSVersion(info *debug.BuildInfo, ok bool, runtime string) error {
	built, err := builtNATSServerVersion(info, ok)
	if err != nil {
		return err
	}
	if built != runtime {
		return fmt.Errorf("%w: gnats.VERSION=%q built=%q", errNATSVersionMismatch, runtime, built)
	}
	return nil
}

func natsDep(version string, replace *debug.Module) *debug.Module {
	return &debug.Module{Path: natsServerModulePath, Version: version, Replace: replace}
}

func buildInfoWith(deps ...*debug.Module) *debug.BuildInfo {
	return &debug.BuildInfo{Deps: deps}
}

func buildInfoGo(goVersion string, deps ...*debug.Module) *debug.BuildInfo {
	return &debug.BuildInfo{GoVersion: goVersion, Deps: deps}
}

// forbidGoList is the go list source for cases where the fallback must not run.
func forbidGoList(t *testing.T) func() (*debug.Module, error) {
	return func() (*debug.Module, error) {
		t.Fatal("go list fallback invoked")
		return nil, nil
	}
}

func TestEmbeddedNATSVersionFence(t *testing.T) {
	other := &debug.Module{Path: "example.com/other", Version: "v9.9.9"}
	tests := []struct {
		name    string
		info    *debug.BuildInfo
		ok      bool
		runtime string
		wantErr error
		wantMsg string
	}{
		{"match", buildInfoWith(other, natsDep("v2.0.1", nil)), true, "2.0.1", nil, ""},
		{"prerelease match", buildInfoWith(natsDep("v2.0.1-rc.1", nil)), true, "2.0.1-rc.1", nil, ""},
		{"mismatch", buildInfoWith(natsDep("v2.0.1", nil)), true, "2.0.2", errNATSVersionMismatch,
			`gnats.VERSION="2.0.2" built="2.0.1"`},
		{"no build info", nil, false, "2.0.1", errNATSBuildInfoMissing, "build information is unavailable"},
		{"ok but nil info", nil, true, "2.0.1", errNATSBuildInfoMissing, "build information is unavailable"},
		{"not ok but info present", buildInfoWith(natsDep("v2.0.1", nil)), false, "2.0.1", errNATSBuildInfoMissing,
			"build information is unavailable"},
		{"module missing", buildInfoWith(other), true, "2.0.1", errNATSModuleMissing, natsServerModulePath},
		{"module duplicated", buildInfoWith(natsDep("v2.0.1", nil), natsDep("v2.0.1", nil)), true, "2.0.1",
			errNATSModuleDuplicated, natsServerModulePath},
		{"nil dependency entries skipped", buildInfoWith(nil, natsDep("v2.0.1", nil)), true, "2.0.1", nil, ""},
		{"same-path replace honored",
			buildInfoWith(natsDep("v2.0.1", &debug.Module{Path: natsServerModulePath, Version: "v2.0.2"})),
			true, "2.0.2", nil, ""},
		{"same-path replace mismatch",
			buildInfoWith(natsDep("v2.0.1", &debug.Module{Path: natsServerModulePath, Version: "v2.0.2"})),
			true, "2.0.1", errNATSVersionMismatch, `built="2.0.2"`},
		{"directory replace", buildInfoWith(natsDep("v2.0.1", &debug.Module{Path: "../nats-server"})), true, "2.0.1",
			errNATSModuleReplaced, "../nats-server"},
		{"fork replace",
			buildInfoWith(natsDep("v2.0.1", &debug.Module{Path: "example.com/fork/nats-server/v2", Version: "v2.0.1"})),
			true, "2.0.1", errNATSModuleReplaced, "example.com/fork/nats-server/v2"},
		{"pseudo-version", buildInfoWith(natsDep("v2.0.2-0.20240102030405-0123456789ab", nil)), true, "2.0.2-0.20240102030405-0123456789ab",
			errNATSVersionMalformed, "pseudo-version"},
		{"pseudo-version after release", buildInfoWith(natsDep("v2.0.2-0.20240102030405-0123456789ab", nil)), true, "2.0.1",
			errNATSVersionMalformed, "pseudo-version"},
		{"+incompatible", buildInfoWith(natsDep("v2.0.1+incompatible", nil)), true, "2.0.1", errNATSVersionMalformed, "v2.0.1+incompatible"},
		{"no v", buildInfoWith(natsDep("2.0.1", nil)), true, "2.0.1", errNATSVersionMalformed, `"2.0.1"`},
		{"vv", buildInfoWith(natsDep("vv2.0.1", nil)), true, "2.0.1", errNATSVersionMalformed, "vv2.0.1"},
		{"v3", buildInfoWith(natsDep("v3.0.1", nil)), true, "3.0.1", errNATSVersionMalformed, "v3.0.1"},
		{"devel", buildInfoWith(natsDep("(devel)", nil)), true, "2.0.1", errNATSVersionMalformed, "(devel)"},
		{"empty", buildInfoWith(natsDep("", nil)), true, "2.0.1", errNATSVersionMalformed, `""`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := checkEmbeddedNATSVersion(tc.info, tc.ok, tc.runtime, forbidGoList(t))
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("error %q lacks %q", err, tc.wantMsg)
			}
		})
	}
}

func TestEmbeddedNATSVersionGoListFallback(t *testing.T) {
	errBoom := errors.New("exit status 1: boom")
	other := &debug.Module{Path: "example.com/other", Version: "v2.0.1"}
	pseudo := "v2.0.2-0.20240102030405-0123456789ab"
	tests := []struct {
		name     string
		info     *debug.BuildInfo
		ok       bool
		listMod  *debug.Module
		listErr  error
		runtime  string
		wantList bool
		wantErr  error
		wantMsg  string
	}{
		{"taken: match", buildInfoGo("go1.26.5"), true, natsDep("v2.0.1", nil), nil, "2.0.1", true, nil, ""},
		{"taken: mismatch", buildInfoGo("go1.26.5"), true, natsDep("v2.0.1", nil), nil, "2.0.2", true,
			errNATSVersionMismatch, `gnats.VERSION="2.0.2" built="2.0.1"`},
		{"taken: go list failure", buildInfoGo("go1.26.5"), true, nil, errBoom, "2.0.1", true, errNATSModuleResolve, "boom"},
		{"taken: nil module", buildInfoGo("go1.26.5"), true, nil, nil, "2.0.1", true, errNATSModuleResolve, "no module"},
		{"taken: wrong module path", buildInfoGo("go1.26.5"), true, other, nil, "2.0.1", true,
			errNATSModuleMissing, "example.com/other"},
		{"taken: directory replace", buildInfoGo("go1.26.5"), true, natsDep("v2.0.1", &debug.Module{Path: "../nats-server"}),
			nil, "2.0.1", true, errNATSModuleReplaced, "../nats-server"},
		{"taken: fork replace", buildInfoGo("go1.26.5"), true,
			natsDep("v2.0.1", &debug.Module{Path: "example.com/fork/nats-server/v2", Version: "v2.0.1"}), nil, "2.0.1", true,
			errNATSModuleReplaced, "example.com/fork/nats-server/v2"},
		{"taken: same-path replace honored", buildInfoGo("go1.26.5"), true,
			natsDep("v2.0.1", &debug.Module{Path: natsServerModulePath, Version: "v2.0.2"}), nil, "2.0.2", true, nil, ""},
		{"taken: pseudo-version", buildInfoGo("go1.26.5"), true, natsDep(pseudo, nil), nil, "2.0.2", true,
			errNATSVersionMalformed, "pseudo-version"},
		{"taken: malformed", buildInfoGo("go1.25.12"), true, natsDep("2.0.1", nil), nil, "2.0.1", true,
			errNATSVersionMalformed, `"2.0.1"`},
		{"refused: not ok", buildInfoGo("go1.26.5"), false, nil, nil, "2.0.1", false, errNATSBuildInfoMissing, "unavailable"},
		{"refused: go1.27.0", buildInfoGo("go1.27.0"), true, nil, nil, "2.0.1", false, errNATSDepsMissing, `"go1.27.0"`},
		{"refused: go1.27.2", buildInfoGo("go1.27.2"), true, nil, nil, "2.0.1", false, errNATSDepsMissing, `"go1.27.2"`},
		{"refused: devel", buildInfoGo("devel go1.28-abc"), true, nil, nil, "2.0.1", false,
			errNATSDepsMissing, `"devel go1.28-abc"`},
		{"refused: empty GoVersion", buildInfoGo(""), true, nil, nil, "2.0.1", false, errNATSDepsMissing, `GoVersion ""`},
		{"refused: deps without nats on go1.26.5", buildInfoGo("go1.26.5", other), true, nil, nil, "2.0.1", false,
			errNATSModuleMissing, natsServerModulePath},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			goList := func() (*debug.Module, error) {
				called = true
				return tc.listMod, tc.listErr
			}
			err := checkEmbeddedNATSVersion(tc.info, tc.ok, tc.runtime, goList)
			if called != tc.wantList {
				t.Fatalf("go list called = %v, want %v", called, tc.wantList)
			}
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) || (tc.listErr != nil && !errors.Is(err, tc.listErr)) {
				t.Fatalf("error = %v, want %v wrapping %v", err, tc.wantErr, tc.listErr)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("error %q lacks %q", err, tc.wantMsg)
			}
		})
	}
}

func TestGoVersionOmitsTestDeps(t *testing.T) {
	for v, want := range map[string]bool{
		"go1.26.5": true, "go1.26.0": true, "go1.26rc1": true, "go1.25.12": true, "go1.21.0": true,
		"go1.27.0": false, "go1.27.2": false, "go1.30.1": false, "go2.0.0": false, "devel go1.28-abc": false,
		"": false, "go1": false, "go1.x": false, " go1.26.5": false,
	} {
		if got := goVersionOmitsTestDeps(v); got != want {
			t.Errorf("goVersionOmitsTestDeps(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestEmbeddedNATSVersionMatchesBuiltDependency(t *testing.T) {
	info, ok := debug.ReadBuildInfo()
	source := "build information"
	goList := func() (*debug.Module, error) {
		source = "go list -m (Go <1.27 test binary embeds no Deps)"
		return goListNATSModule()
	}
	built, err := builtNATSServerVersion(info, ok, goList)
	if err != nil {
		t.Fatalf("built NATS version: %v", err)
	}
	if built != gnats.VERSION {
		t.Fatalf("gnats.VERSION=%q built=%q", gnats.VERSION, built)
	}
	t.Logf("embedded NATS server version %s is bound to the built dependency via %s", built, source)
}

var readmeNATSVersionClaim = regexp.MustCompile(`nats-server/v2[^\n]*\d+\.\d+\.\d+`)

func TestHandshakeInteropREADMEBindsNATSVersion(t *testing.T) {
	raw, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	readme := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if claim := readmeNATSVersionClaim.FindString(readme); claim != "" {
		t.Fatalf("README hard-codes a NATS version: %q", claim)
	}
	for _, want := range []string{"debug.ReadBuildInfo", "gnats.VERSION", "go list -m"} {
		if !strings.Contains(readme, want) {
			t.Fatalf("README does not describe the dependency-bound assertion: missing %q", want)
		}
	}
}
