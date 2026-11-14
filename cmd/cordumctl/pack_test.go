package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
)

func TestPackServerInstall_SignedPackLifecycle(t *testing.T) {
	root, trust := packServerFixture(t)
	for _, action := range []string{"install", "upgrade", "uninstall", "refusal", "force-inactive"} {
		t.Run(action, func(t *testing.T) { packServerLifecycle(t, root, trust, action) })
	}
}

func packServerFixture(t *testing.T) (string, string) {
	t.Helper()
	root, trust := signInstallTestPack(t, "pack-test")
	manifestPath := filepath.Join(root, "pack.yaml")
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	// The signing fixture is format-only; add install-compatible names/version.
	text := strings.ReplaceAll(string(manifest), "t/In", "toolchain-test/In")
	text = strings.ReplaceAll(text, "t.echo", "toolchain-test.echo")
	text += fmt.Sprintf("compatibility:\n  protocolVersion: %d\n", capsdk.DefaultProtocolVersion)
	if err := os.WriteFile(manifestPath, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	keyPath, pub := writeTestPrivateKey(t)
	if err := runPackSign([]string{"--key", keyPath, root}); err != nil {
		t.Fatal(err)
	}
	key := fmt.Sprintf("kid: pack-test\nalgorithm: ed25519\npublic_key_b64: %s\n", base64.StdEncoding.EncodeToString(pub))
	if err := os.WriteFile(filepath.Join(trust, "pack-test.pub"), []byte(key), 0600); err != nil {
		t.Fatal(err)
	}
	return root, trust
}

func packServerLifecycle(t *testing.T, root, trust, action string) {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		packServerRequest(t, w, r, root, action)
	}))
	defer srv.Close()
	args := []string{"--gateway", srv.URL, "--api-key", "test-key", "--tenant", "test-tenant"}
	var err error
	var output string
	stderr := captureStderr(t, func() {
		output = captureStdout(t, func() {
			if action == "uninstall" {
				err = runPackUninstall(append(args, "--purge", "toolchain/test ?"))
				return
			}
			args = append(args, "--strict", "--trusted-keys", trust)
			if action == "upgrade" {
				args = append(args, "--upgrade")
			}
			if action == "force-inactive" {
				args = append(args, "--force", "--inactive", "--no-verify")
			}
			err = runPackInstall(append(args, root))
		})
	})
	if calls != 1 {
		t.Errorf("requests=%d, want exactly one pack API call", calls)
	}
	if action == "refusal" {
		if err == nil || !strings.Contains(err.Error(), `"code":"PACK_INSTALL_INVALID"`) || !strings.Contains(err.Error(), "pack.unsigned: required") || output != "" {
			t.Errorf("refusal: err=%v output=%q", err, output)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	packServerOutput(t, action, output, stderr)
}

func packServerOutput(t *testing.T, action, output, stderr string) {
	t.Helper()
	want := "installed pack toolchain-test 0.1.0 (ACTIVE)\nworker_id: pack-worker\nworker_token: test-worker-token\n"
	if action == "uninstall" {
		want = "uninstalled pack toolchain/test ?\n"
	}
	if output != want || strings.Contains(stderr, "test-worker-token") {
		t.Errorf("output=%q stderr=%q", output, stderr)
	}
}

func packServerRequest(t *testing.T, w http.ResponseWriter, r *http.Request, root, action string) {
	t.Helper()
	if r.Header.Get("X-API-Key") != "test-key" || r.Header.Get("X-Tenant-ID") != "test-tenant" {
		t.Error("credential headers not forwarded")
	}
	w.Header().Set("Content-Type", "application/json")
	if action == "uninstall" && r.Method == "POST" && r.URL.EscapedPath() == "/api/v1/packs/"+url.PathEscape("toolchain/test ?")+"/uninstall" {
		var body map[string]bool
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !reflect.DeepEqual(body, map[string]bool{"purge": true}) {
			t.Errorf("purge: body=%v err=%v", body, err)
		}
		w.WriteHeader(204)
		return
	}
	if r.Method != "POST" || r.URL.Path != "/api/v1/packs/install" {
		t.Errorf("unexpected client request: %s %s", r.Method, r.URL)
		http.Error(w, "pack API required", 400)
		return
	}
	if err := packServerMultipart(root, r, action); err != nil {
		t.Error(err)
		http.Error(w, "invalid install", 400)
		return
	}
	if action == "refusal" {
		w.WriteHeader(400)
		_, _ = io.WriteString(w, `{"code":"PACK_INSTALL_INVALID","error":"pack.unsigned: required"}`)
		return
	}
	_, _ = io.WriteString(w, `{"id":"toolchain-test","version":"0.1.0","status":"ACTIVE","verification":{"signed":true},"worker_credential":{"worker_id":"pack-worker","token":"test-worker-token","allowed_pools":["test"],"allowed_topics":["job.test"]}}`)
}

func packServerMultipart(root string, r *http.Request, action string) error {
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		return err
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()
	for _, field := range []string{"force", "upgrade", "inactive"} {
		want := "false"
		if (field == "upgrade" && action == "upgrade") || (field != "upgrade" && action == "force-inactive") {
			want = "true"
		}
		if values := r.MultipartForm.Value[field]; !reflect.DeepEqual(values, []string{want}) {
			return fmt.Errorf("%s=%v want [%s]", field, values, want)
		}
	}
	file, header, err := r.FormFile("bundle")
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	if header.Filename != "bundle.tgz" {
		return fmt.Errorf("filename=%s", header.Filename)
	}
	return packServerArchiveMatches(root, file)
}

func packServerArchiveMatches(root string, reader io.Reader) error {
	gz, err := gzip.NewReader(reader)
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()
	got := map[string]string{}
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if strings.Contains(header.Name, "\\") || strings.HasPrefix(header.Name, "./") {
			return fmt.Errorf("non-portable archive name %q", header.Name)
		}
		if header.Typeflag == tar.TypeDir {
			continue
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return err
		}
		if _, exists := got[header.Name]; exists {
			return fmt.Errorf("duplicate archive entry %s", header.Name)
		}
		got[header.Name] = string(data)
	}
	want, err := packServerFiles(root)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(got, want) {
		return fmt.Errorf("archive files differ from verified root")
	}
	return nil
}

func packServerFiles(root string) (map[string]string, error) {
	want := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		want[filepath.ToSlash(rel)] = string(data)
		return err
	})
	return want, err
}

func TestPackArchiveDeterministicAndPortable(t *testing.T) {
	root := writeTestPack(t)
	first, err := packArchive(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(root, "pack.yaml"), time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	second, err := packArchive(root)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatalf("non-deterministic archive: %v", err)
	}
	if err := packServerArchiveMatches(root, bytes.NewReader(first)); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"archive", "url"} {
		t.Run(source, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "pack.tgz")
			if err := os.WriteFile(path, first, 0600); err != nil {
				t.Fatal(err)
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(first) }))
			defer srv.Close()
			if source == "url" {
				path = srv.URL + "/pack.tgz"
			}
			bundle, err := loadPackBundle(path, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer bundle.Cleanup()
			archive, err := packArchive(bundle.Dir)
			if err != nil {
				t.Fatal(err)
			}
			if err := packServerArchiveMatches(root, bytes.NewReader(archive)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPackArchiveRefusesInvalidEntries(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		if data, err := packArchive(filepath.Join(t.TempDir(), "missing")); err == nil || data != nil {
			t.Fatalf("missing root: bytes=%d err=%v", len(data), err)
		}
	})
	t.Run("symlink", func(t *testing.T) {
		root := writeTestPack(t)
		path := filepath.Join(root, "link")
		if err := os.Symlink(filepath.Join(root, "pack.yaml"), path); err != nil {
			if runtime.GOOS == "windows" {
				t.Skipf("Windows symlink privilege unavailable: %v", err)
			}
			t.Fatal(err)
		}
		if data, err := packArchive(root); err == nil || data != nil || !strings.Contains(err.Error(), path) {
			t.Fatalf("symlink: bytes=%d err=%v", len(data), err)
		}
	})
	t.Run("oversized", func(t *testing.T) {
		root := t.TempDir()
		file, err := os.Create(filepath.Join(root, "large"))
		if err != nil {
			t.Fatal(err)
		}
		err = file.Truncate(maxPackFileBytes + 1)
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			t.Fatalf("fixture: %v %v", err, closeErr)
		}
		if data, err := packArchive(root); err == nil || data != nil {
			t.Fatalf("oversized file: bytes=%d err=%v", len(data), err)
		}
	})
}

type packRoundTrip func(*http.Request) (*http.Response, error)

func (f packRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPackInstallTransportTimeoutAndResult(t *testing.T) {
	client := newRestClient("http://gateway.invalid", "", "", nil)
	remaining := time.Duration(0)
	client.httpClient.Transport = packRoundTrip(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok {
			t.Error("missing request timeout")
		}
		remaining = time.Until(deadline)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"p","verification":{"signed":true},"worker_credential":{"allowed_pools":["pool"],"allowed_topics":["topic"]}}`)), Header: make(http.Header)}, nil
	})
	result, err := client.installPack(context.Background(), nil, false, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if remaining < 110*time.Second || remaining > 2*time.Minute || client.httpClient.Timeout != 20*time.Second {
		t.Fatalf("install timeout=%v shared=%v", remaining, client.httpClient.Timeout)
	}
	if result.ID != "p" || result.Verification == nil || !result.Verification.Signed || result.WorkerCredential == nil || !reflect.DeepEqual(result.WorkerCredential.AllowedPools, []string{"pool"}) || !reflect.DeepEqual(result.WorkerCredential.AllowedTopics, []string{"topic"}) {
		t.Fatalf("result=%+v", result)
	}
	if err := client.doJSON(context.Background(), "GET", "/status", nil, nil); err != nil {
		t.Fatal(err)
	}
	if remaining < 15*time.Second || remaining > 20*time.Second {
		t.Fatalf("JSON timeout=%v", remaining)
	}
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()

	original := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	os.Stderr = w
	defer func() {
		os.Stderr = original
	}()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close stderr writer: %v", err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close stderr reader: %v", err)
	}
	return string(data)
}

func TestValidatePackManifest(t *testing.T) {
	if err := validatePackManifest(nil); err == nil {
		t.Fatalf("expected error for nil manifest")
	}
	manifest := &packManifest{}
	if err := validatePackManifest(manifest); err == nil {
		t.Fatalf("expected error for missing metadata")
	}

	manifest = &packManifest{
		Metadata: packMetadata{ID: "Bad ID", Version: "1.0.0"},
	}
	if err := validatePackManifest(manifest); err == nil {
		t.Fatalf("expected error for invalid id")
	}

	manifest = &packManifest{
		Metadata: packMetadata{ID: "pack1", Version: ""},
	}
	if err := validatePackManifest(manifest); err == nil {
		t.Fatalf("expected error for missing version")
	}

	manifest = &packManifest{
		Metadata: packMetadata{ID: "pack1", Version: "1.0.0"},
		Topics:   []packTopic{{Name: "job.other.topic"}},
	}
	if err := validatePackManifest(manifest); err == nil {
		t.Fatalf("expected error for un-namespaced topic")
	}

	manifest = &packManifest{
		Metadata: packMetadata{ID: "pack1", Version: "1.0.0"},
		Topics:   []packTopic{{Name: "job.pack1.topic"}},
		Resources: packResources{
			Schemas:   []packResource{{ID: "schema", Path: "schemas/a.json"}},
			Workflows: []packResource{{ID: "workflow", Path: "workflows/a.json"}},
		},
	}
	if err := validatePackManifest(manifest); err == nil {
		t.Fatalf("expected error for un-namespaced resources")
	}

	manifest = &packManifest{
		Metadata: packMetadata{ID: "pack1", Version: "1.0.0"},
		Topics:   []packTopic{{Name: "job.pack1.topic"}},
		Resources: packResources{
			Schemas:   []packResource{{ID: "pack1/schema", Path: "schemas/a.json"}},
			Workflows: []packResource{{ID: "pack1.workflow", Path: "workflows/a.json"}},
		},
	}
	if err := validatePackManifest(manifest); err != nil {
		t.Fatalf("expected valid manifest: %v", err)
	}

	manifest = &packManifest{
		Metadata: packMetadata{ID: "pack1", Version: "1.0.0"},
		Compatibility: packCompatibility{
			MinCoreVersion: "not-a-version",
		},
		Topics: []packTopic{{Name: "job.pack1.topic"}},
		Resources: packResources{
			Schemas:   []packResource{{ID: "pack1/schema", Path: "schemas/a.json"}},
			Workflows: []packResource{{ID: "pack1.workflow", Path: "workflows/a.json"}},
		},
	}
	if err := validatePackManifest(manifest); err == nil {
		t.Fatalf("expected error for invalid minCoreVersion")
	}

	manifest.Compatibility.MinCoreVersion = "0.6.0"
	manifest.Compatibility.MaxCoreVersion = "1.2.3"
	if err := validatePackManifest(manifest); err != nil {
		t.Fatalf("expected valid core version constraints: %v", err)
	}
}

func TestValidatePackManifestTopicSchemaRefs(t *testing.T) {
	manifest := &packManifest{
		Metadata: packMetadata{ID: "pack1", Version: "1.0.0"},
		Topics: []packTopic{
			{
				Name:           "job.pack1.topic",
				InputSchemaID:  "pack1/Input",
				OutputSchemaID: "pack1/Output",
			},
		},
		Resources: packResources{
			Schemas: []packResource{
				{ID: "pack1/Input", Path: "schemas/input.json"},
				{ID: "pack1/Output", Path: "schemas/output.json"},
			},
			Workflows: []packResource{
				{ID: "pack1.workflow", Path: "workflows/workflow.yaml"},
			},
		},
	}

	if err := validatePackManifest(manifest); err != nil {
		t.Fatalf("expected valid schema refs, got: %v", err)
	}

	manifest.Topics[0].InputSchemaID = "pack1/Missing"
	err := validatePackManifest(manifest)
	if err == nil {
		t.Fatal("expected missing schema ref error")
	}
	if !strings.Contains(err.Error(), "topic job.pack1.topic references unknown schema pack1/Missing") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEnsureProtocolCompatible(t *testing.T) {
	manifest := &packManifest{}
	if err := ensureProtocolCompatible(manifest); err == nil {
		t.Fatalf("expected error for missing protocol")
	}
	manifest.Compatibility.ProtocolVersion = capsdk.DefaultProtocolVersion + 1
	if err := ensureProtocolCompatible(manifest); err == nil {
		t.Fatalf("expected error for mismatched protocol")
	}
	manifest.Compatibility.ProtocolVersion = capsdk.DefaultProtocolVersion
	if err := ensureProtocolCompatible(manifest); err != nil {
		t.Fatalf("expected protocol match: %v", err)
	}
}

func TestNormalizeDecision(t *testing.T) {
	cases := map[string]string{
		"allow":                  "ALLOW",
		"DECISION_TYPE_DENY":     "DENY",
		"require_human":          "REQUIRE_APPROVAL",
		"allow_with_constraints": "ALLOW_WITH_CONSTRAINTS",
		"DECISION_TYPE_THROTTLE": "THROTTLE",
		"custom":                 "CUSTOM",
	}
	for raw, expect := range cases {
		if got := normalizeDecision(raw); got != expect {
			t.Fatalf("decision %s expected %s got %s", raw, expect, got)
		}
	}
}

func TestRestClientEscapesResourceIDs(t *testing.T) {
	var gotRawPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Use EscapedPath to preserve percent-encoding.
		gotRawPaths = append(gotRawPaths, r.URL.EscapedPath())
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"schema":{}}`))
	}))
	defer srv.Close()

	c := &restClient{baseURL: srv.URL, httpClient: srv.Client()}
	ctx := context.Background()

	// IDs with special characters that could cause path traversal.
	dangerousIDs := []string{"../etc/passwd", "id with spaces", "slashes/in/id"}

	for _, id := range dangerousIDs {
		gotRawPaths = nil
		if _, err := c.getSchema(ctx, id); err != nil {
			t.Fatal(err)
		}
		if _, err := c.getWorkflow(ctx, id); err != nil {
			t.Fatal(err)
		}
		if err := c.uninstallPack(ctx, id, false); err != nil {
			t.Fatal(err)
		}
		want := []string{"/api/v1/schemas/" + url.PathEscape(id), "/api/v1/workflows/" + url.PathEscape(id), "/api/v1/packs/" + url.PathEscape(id) + "/uninstall"}
		if !reflect.DeepEqual(gotRawPaths, want) {
			t.Errorf("paths=%v want=%v", gotRawPaths, want)
		}

		for _, p := range gotRawPaths {
			// After PathEscape, "../" becomes "..%2F" and spaces become "%20".
			if strings.Contains(p, "../") || strings.Contains(p, " ") {
				t.Errorf("path not properly escaped: %s (from id %q)", p, id)
			}
		}
	}
}

func TestRunPackInstallDryRunWithTopicSchemaBindings(t *testing.T) {
	packDir := t.TempDir()
	manifest := fmt.Sprintf(`
apiVersion: cordum.io/v1
kind: Pack
metadata:
  id: demo-pack
  version: 1.0.0
compatibility:
  protocolVersion: %d
topics:
  - name: job.demo-pack.echo
    capability: demo.echo
    inputSchema: demo-pack/Input
    outputSchema: demo-pack/Output
resources:
  schemas:
    - id: demo-pack/Input
      path: schemas/input.json
    - id: demo-pack/Output
      path: schemas/output.json
  workflows:
    - id: demo-pack.echo
      path: workflows/echo.yaml
`, capsdk.DefaultProtocolVersion)
	if err := os.WriteFile(filepath.Join(packDir, "pack.yaml"), []byte(strings.TrimSpace(manifest)), 0o600); err != nil {
		t.Fatalf("write pack manifest: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(packDir, "schemas"), 0o700); err != nil {
		t.Fatalf("mkdir schemas: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(packDir, "workflows"), 0o700); err != nil {
		t.Fatalf("mkdir workflows: %v", err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "schemas", "input.json"), []byte(`{"type":"object","required":["message"],"properties":{"message":{"type":"string"}}}`), 0o600); err != nil {
		t.Fatalf("write input schema: %v", err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "schemas", "output.json"), []byte(`{"type":"object","required":["message"],"properties":{"message":{"type":"string"}}}`), 0o600); err != nil {
		t.Fatalf("write output schema: %v", err)
	}
	workflow := `
id: demo-pack.echo
name: Demo Echo
version: "0.1.0"
steps:
  echo:
    id: echo
    name: Echo
    type: worker
    topic: job.demo-pack.echo
    input:
      message: ${input.message}
    input_schema_id: demo-pack/Input
    output_schema_id: demo-pack/Output
    meta:
      pack_id: demo-pack
      capability: demo.echo
      risk_tags: []
      requires: []
`
	if err := os.WriteFile(filepath.Join(packDir, "workflows", "echo.yaml"), []byte(strings.TrimSpace(workflow)), 0o600); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/schemas/"):
			http.NotFound(w, r)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/workflows/"):
			http.NotFound(w, r)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()

	if err := runPackInstall([]string{"--gateway", srv.URL, "--force", "--dry-run", packDir}); err != nil {
		t.Fatalf("expected dry-run install to succeed with topic schema bindings, got: %v", err)
	}
}

func TestLoadPackRegistryInitializesNilDataMap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/config" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"scope":"system","scope_id":"packs","data":null}`))
	}))
	defer srv.Close()

	client := &restClient{baseURL: srv.URL, httpClient: srv.Client()}
	records, doc, err := loadPackRegistry(context.Background(), client)
	if err != nil {
		t.Fatalf("loadPackRegistry returned error: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("expected empty records map, got %d entries", len(records))
	}
	if doc == nil {
		t.Fatalf("expected config doc")
	}
	if doc.Data == nil {
		t.Fatalf("expected doc.Data to be initialized, got nil")
	}
}
