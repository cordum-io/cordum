package main

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestParseHandlerPattern(t *testing.T) {
	cases := []struct {
		in         string
		wantMethod string
		wantPath   string
		wantErr    bool
	}{
		{"GET /foo", "get", "/foo", false},
		{"POST /api/v1/bar", "post", "/api/v1/bar", false},
		{"  DELETE   /with/{id}  ", "delete", "/with/{id}", false},
		{"/any", "", "/any", false},
		{"", "", "", true},
		{"GET", "", "", true},
		{"GET foo", "", "", true},
	}
	for _, c := range cases {
		m, p, err := parseHandlerPattern(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("%q: expected error, got %q %q", c.in, m, p)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: unexpected error %v", c.in, err)
			continue
		}
		if m != c.wantMethod || p != c.wantPath {
			t.Errorf("%q: got (%q,%q) want (%q,%q)", c.in, m, p, c.wantMethod, c.wantPath)
		}
	}
}

func TestNormalisePath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/foo", "/foo"},
		{"/foo/{id}", "/foo/{}"},
		{"/foo/{name}/bar", "/foo/{}/bar"},
		{"/foo/{a}/bar/{b}", "/foo/{}/bar/{}"},
	}
	for _, c := range cases {
		if got := normalisePath(c.in); got != c.want {
			t.Errorf("normalisePath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCollectRoutes_MultiplePackages(t *testing.T) {
	dir := t.TempDir()
	// Two sub-packages under the gateway-like dir. The walker should pick up
	// routes from both, and should NOT pick up any from the _test.go file.
	mustWrite(t, filepath.Join(dir, "pkga", "routes.go"), `
package pkga

import "net/http"

func register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/foo", nil)
	mux.HandleFunc("POST /api/v1/foo/{id}", nil)
	mux.HandleFunc("/api/v1/stream", nil)
}
`)
	mustWrite(t, filepath.Join(dir, "pkgb", "more.go"), `
package pkgb

import "net/http"

func register(mux *http.ServeMux) {
	mux.HandleFunc(`+"`DELETE /api/v1/other/{name}`"+`, nil)
}
`)
	mustWrite(t, filepath.Join(dir, "pkgb", "ignore_test.go"), `
package pkgb

import "net/http"

func registerSyntheticTestRoute(mux *http.ServeMux) {
	mux.HandleFunc("GET /should/not/appear", nil)
}
`)

	got, err := collectRoutes(dir)
	if err != nil {
		t.Fatalf("collectRoutes: %v", err)
	}
	sort.Slice(got, func(i, j int) bool {
		if got[i].Path != got[j].Path {
			return got[i].Path < got[j].Path
		}
		return got[i].Method < got[j].Method
	})
	want := []Route{
		{Method: "get", Path: "/api/v1/foo"},
		{Method: "post", Path: "/api/v1/foo/{id}"},
		{Method: "delete", Path: "/api/v1/other/{name}"},
		{Method: "", Path: "/api/v1/stream"},
	}
	sort.Slice(want, func(i, j int) bool {
		if want[i].Path != want[j].Path {
			return want[i].Path < want[j].Path
		}
		return want[i].Method < want[j].Method
	})
	if len(got) != len(want) {
		t.Fatalf("got %d routes, want %d. routes=%+v", len(got), len(want), got)
	}
	for i := range got {
		if got[i].Method != want[i].Method || got[i].Path != want[i].Path {
			t.Errorf("routes[%d]: got %+v want %+v", i, got[i], want[i])
		}
		if got[i].File == "" || got[i].Line == 0 {
			t.Errorf("routes[%d] missing file/line metadata: %+v", i, got[i])
		}
	}
	for _, r := range got {
		if strings.Contains(r.Path, "should/not/appear") {
			t.Fatalf("_test.go route was included: %+v", r)
		}
	}
}

func TestLoadSpecOps_ZeroOperationsErrors(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.yaml")
	mustWrite(t, specPath, `
openapi: 3.0.3
info: {title: t, version: "1"}
paths:
  /nope:
    description: has a description, no operations
`)
	if _, _, err := loadSpecOps(specPath); err == nil {
		t.Fatalf("expected error for path with no operations")
	}
}

func TestLoadSpecOps_MalformedYAMLErrorsCleanly(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.yaml")
	mustWrite(t, specPath, "paths: [this is not a mapping")
	_, _, err := loadSpecOps(specPath)
	if err == nil {
		t.Fatalf("expected error for malformed YAML")
	}
	if strings.Contains(err.Error(), "panic") {
		t.Fatalf("error message hints at panic: %v", err)
	}
}

func TestDiff_PathParameterNormalisation(t *testing.T) {
	// Route uses {id}; spec uses {user_id}. They must match after normalisation.
	routes := []Route{{Method: "get", Path: "/users/{id}"}}
	ops := []SpecOp{{Method: "get", Path: "/users/{user_id}"}}
	missing, unrouted := diff(routes, ops, nil)
	if len(missing) != 0 || len(unrouted) != 0 {
		t.Fatalf("expected clean diff, got missing=%+v unrouted=%+v", missing, unrouted)
	}
}

func TestDiff_RouteMissingFromSpec(t *testing.T) {
	routes := []Route{
		{Method: "get", Path: "/a"},
		{Method: "post", Path: "/a"},
	}
	ops := []SpecOp{{Method: "get", Path: "/a"}}
	missing, unrouted := diff(routes, ops, nil)
	if len(missing) != 1 || missing[0].Method != "post" {
		t.Fatalf("expected POST /a missing, got %+v", missing)
	}
	if len(unrouted) != 0 {
		t.Fatalf("expected no unrouted, got %+v", unrouted)
	}
}

func TestDiff_SpecOpWithoutRoute(t *testing.T) {
	routes := []Route{{Method: "get", Path: "/a"}}
	ops := []SpecOp{
		{Method: "get", Path: "/a"},
		{Method: "delete", Path: "/a"},
	}
	missing, unrouted := diff(routes, ops, nil)
	if len(missing) != 0 {
		t.Fatalf("expected no missing, got %+v", missing)
	}
	if len(unrouted) != 1 || unrouted[0].Method != "delete" {
		t.Fatalf("expected DELETE /a unrouted, got %+v", unrouted)
	}
}

func TestDiff_AnyMethodRouteRequiresXAnyMethodOrAllMethods(t *testing.T) {
	routes := []Route{{Method: "", Path: "/stream"}}

	// Case A: spec declares only GET — any-method route is NOT satisfied.
	missing, _ := diff(routes, []SpecOp{{Method: "get", Path: "/stream"}}, nil)
	if len(missing) != 1 {
		t.Fatalf("expected any-method route flagged when spec only lists GET, got %+v", missing)
	}

	// Case B: spec declares x-any-method on the path — any-method route IS satisfied.
	missing, _ = diff(
		[]Route{{Method: "", Path: "/stream"}},
		[]SpecOp{{Method: "get", Path: "/stream"}},
		map[string]bool{"/stream": true},
	)
	if len(missing) != 0 {
		t.Fatalf("expected clean diff with x-any-method, got %+v", missing)
	}

	// Case C: spec enumerates every standard method — any-method route is satisfied.
	var ops []SpecOp
	for _, m := range standardMethods {
		ops = append(ops, SpecOp{Method: m, Path: "/stream"})
	}
	missing, _ = diff(routes, ops, nil)
	if len(missing) != 0 {
		t.Fatalf("expected clean diff with all-methods spec, got %+v", missing)
	}
}

func TestRun_EndToEndExitZero(t *testing.T) {
	dir := t.TempDir()
	gwDir := filepath.Join(dir, "gateway")
	mustWrite(t, filepath.Join(gwDir, "r.go"), `
package gateway
import "net/http"
func register(mux *http.ServeMux) {
	mux.HandleFunc("GET /foo", nil)
	mux.HandleFunc("POST /bar/{id}", nil)
}
`)
	specPath := filepath.Join(dir, "spec.yaml")
	mustWrite(t, specPath, `
openapi: 3.0.3
info: {title: t, version: "1"}
paths:
  /foo:
    get: {summary: foo}
  /bar/{id}:
    post: {summary: bar}
`)
	stdout, stderr := tempFile(t), tempFile(t)
	code := run(specPath, gwDir, false, stdout, stderr)
	if code != 0 {
		t.Fatalf("exit=%d, stderr=%s", code, readFile(t, stderr.Name()))
	}
}

func TestRun_EndToEndExitOneOnGap(t *testing.T) {
	dir := t.TempDir()
	gwDir := filepath.Join(dir, "gateway")
	mustWrite(t, filepath.Join(gwDir, "r.go"), `
package gateway
import "net/http"
func register(mux *http.ServeMux) {
	mux.HandleFunc("GET /foo", nil)
}
`)
	specPath := filepath.Join(dir, "spec.yaml")
	mustWrite(t, specPath, `
openapi: 3.0.3
info: {title: t, version: "1"}
paths:
  /unrouted:
    get: {summary: x}
`)
	stdout, stderr := tempFile(t), tempFile(t)
	code := run(specPath, gwDir, true, stdout, stderr)
	if code != 1 {
		t.Fatalf("expected exit 1 on gap, got %d", code)
	}
	out := readFile(t, stdout.Name())
	if !bytes.Contains([]byte(out), []byte("/foo")) || !bytes.Contains([]byte(out), []byte("/unrouted")) {
		t.Fatalf("expected report to mention both gaps, got:\n%s", out)
	}
}

func TestLoadSpecOps_DeprecatedOpsSkipped(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.yaml")
	mustWrite(t, specPath, `
openapi: 3.0.3
info: {title: t, version: "1"}
paths:
  /live:
    get: {summary: live}
  /gone:
    get:
      deprecated: true
      summary: gone
`)
	ops, _, err := loadSpecOps(specPath)
	if err != nil {
		t.Fatalf("loadSpecOps: %v", err)
	}
	if len(ops) != 1 || ops[0].Path != "/live" {
		t.Fatalf("expected only /live to survive deprecated filter, got %+v", ops)
	}
}

func TestDiff_AnyMethodRouteCoversEverySpecOp(t *testing.T) {
	// When the gateway registers /x as any-method and the spec lists GET /x,
	// the spec op is considered covered (the route WILL handle GET).
	routes := []Route{{Method: "", Path: "/x"}}
	ops := []SpecOp{{Method: "get", Path: "/x"}}
	_, unrouted := diff(routes, ops, map[string]bool{"/x": true})
	if len(unrouted) != 0 {
		t.Fatalf("expected any-method route to cover GET spec op, got %+v", unrouted)
	}
}

func TestLoadSpecOps_SubrouteDispatchMarksTrailingSlashPrefix(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.yaml")
	mustWrite(t, specPath, `
openapi: 3.0.3
info: {title: t, version: "1"}
paths:
  /datasets:
    x-subroute-dispatch: true
    get: {summary: list}
  /datasets/{id}/runs:
    get: {summary: list runs}
`)
	ops, anyMethod, err := loadSpecOps(specPath)
	if err != nil {
		t.Fatalf("loadSpecOps: %v", err)
	}
	if !anyMethod["/datasets/"] {
		t.Fatalf("expected x-subroute-dispatch to register /datasets/ as delegated prefix, got %+v", anyMethod)
	}
	routes := []Route{
		{Method: "get", Path: "/datasets"},
		{Method: "", Path: "/datasets/"},
	}
	_, unrouted := diff(routes, ops, anyMethod)
	if len(unrouted) != 0 {
		t.Fatalf("expected delegated prefix route to cover child spec ops, got %+v", unrouted)
	}
}

// --- helpers ---

func mustWrite(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimLeft(contents, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
}

func tempFile(t *testing.T) *os.File {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "out-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.Close(); err != nil {
			t.Logf("warning: failed to close temp file: %v", err)
		}
	})
	return f
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
