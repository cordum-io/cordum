package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
)

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

func TestOverlayHelpers(t *testing.T) {
	overlay := packConfigOverlay{Key: "pools"}
	if !shouldSkipConfigOverlay(true, overlay) {
		t.Fatalf("expected pools overlay to be skipped when inactive")
	}
	if shouldSkipConfigOverlay(false, overlay) {
		t.Fatalf("did not expect skip when active")
	}
	overlays := []packAppliedConfigOverlay{{Key: "timeouts"}, {Key: "pools"}}
	if !hasPoolOverlay(overlays) {
		t.Fatalf("expected pool overlay detected")
	}
}

func TestPolicyFragmentID(t *testing.T) {
	if got := policyFragmentID("pack1", ""); got != "pack1/default" {
		t.Fatalf("unexpected fragment id: %s", got)
	}
	if got := policyFragmentID("pack1", "custom"); got != "pack1/custom" {
		t.Fatalf("unexpected fragment id: %s", got)
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

func TestRecordsToAny(t *testing.T) {
	records := map[string]packRecord{
		"pack1": {ID: "pack1", Version: "1.0.0", Status: "ACTIVE"},
	}
	out := recordsToAny(records)
	if _, ok := out["pack1"]; !ok {
		t.Fatalf("expected pack record in map")
	}
}

func TestRecordsToAnyPreservesTestFields(t *testing.T) {
	expectedTests := packTests{
		PolicySimulations: []packPolicySimulation{
			{
				Name: "allow namespaced pack topic",
				Request: packPolicySimulationRequest{
					TenantId:   "tenant-1",
					Topic:      "job.pack1.topic",
					Capability: "pack1.capability",
					RiskTags:   []string{"trusted", "internal"},
					Requires:   []string{"approval"},
					PackId:     "pack1",
					ActorId:    "actor-1",
					ActorType:  "service",
				},
				ExpectDecision: "ALLOW",
			},
		},
	}
	records := map[string]packRecord{
		"pack1": {
			ID:      "pack1",
			Version: "1.0.0",
			Status:  "ACTIVE",
			Tests:   expectedTests,
		},
	}

	out := recordsToAny(records)
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal round-trip payload: %v", err)
	}

	jsonText := string(data)
	for _, key := range []string{
		`"policySimulations"`,
		`"name"`,
		`"request"`,
		`"expectDecision"`,
		`"tenantId"`,
		`"topic"`,
		`"capability"`,
		`"riskTags"`,
		`"requires"`,
		`"packId"`,
		`"actorId"`,
		`"actorType"`,
	} {
		if !strings.Contains(jsonText, key) {
			t.Fatalf("expected JSON output to contain %s: %s", key, jsonText)
		}
	}
	for _, key := range []string{
		`"PolicySimulations"`,
		`"Name"`,
		`"Request"`,
		`"ExpectDecision"`,
		`"TenantId"`,
		`"Topic"`,
		`"Capability"`,
		`"RiskTags"`,
		`"Requires"`,
		`"PackId"`,
		`"ActorId"`,
		`"ActorType"`,
	} {
		if strings.Contains(jsonText, key) {
			t.Fatalf("expected JSON output to omit %s: %s", key, jsonText)
		}
	}

	var roundTrip map[string]packRecord
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		t.Fatalf("unmarshal round-trip payload: %v", err)
	}

	got, ok := roundTrip["pack1"]
	if !ok {
		t.Fatalf("expected pack1 record in round-trip payload")
	}
	if !reflect.DeepEqual(got.Tests, expectedTests) {
		t.Fatalf("unexpected round-trip tests:\nwant: %#v\ngot:  %#v", expectedTests, got.Tests)
	}
}

func TestValidatePoolsPatch(t *testing.T) {
	patch := map[string]any{
		"topics": map[string]any{"job.bad": map[string]any{}},
	}
	if err := validatePoolsPatch(patch, "pack1", nil); err == nil {
		t.Fatalf("expected namespacing error")
	}

	patch = map[string]any{
		"pools": map[string]any{"shared": map[string]any{}},
	}
	if err := validatePoolsPatch(patch, "pack1", nil); err == nil {
		t.Fatalf("expected pool namespacing error")
	}

	current := map[string]any{"pools": map[string]any{"shared": map[string]any{}}}
	if err := validatePoolsPatch(patch, "pack1", current); err != nil {
		t.Fatalf("expected existing pool to be allowed: %v", err)
	}

	patch = map[string]any{"topics": map[string]any{"job.pack1.ok": map[string]any{}}, "extra": 1}
	if err := validatePoolsPatch(patch, "pack1", nil); err == nil {
		t.Fatalf("expected unsupported key error")
	}
}

func TestValidateTimeoutsPatch(t *testing.T) {
	patch := map[string]any{
		"topics": map[string]any{"job.bad": map[string]any{}},
	}
	if err := validateTimeoutsPatch(patch, "pack1"); err == nil {
		t.Fatalf("expected namespacing error")
	}

	patch = map[string]any{
		"workflows": map[string]any{"bad.workflow": map[string]any{}},
	}
	if err := validateTimeoutsPatch(patch, "pack1"); err == nil {
		t.Fatalf("expected workflow namespacing error")
	}

	patch = map[string]any{"topics": map[string]any{"job.pack1.ok": map[string]any{}}, "extra": 1}
	if err := validateTimeoutsPatch(patch, "pack1"); err == nil {
		t.Fatalf("expected unsupported key error")
	}

	patch = map[string]any{
		"topics":    map[string]any{"job.pack1.ok": map[string]any{}},
		"workflows": map[string]any{"pack1.workflow": map[string]any{}},
	}
	if err := validateTimeoutsPatch(patch, "pack1"); err != nil {
		t.Fatalf("expected valid timeouts patch: %v", err)
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
		_, _ = c.getSchema(ctx, id)
		_ = c.deleteSchema(ctx, id)
		_, _ = c.getWorkflow(ctx, id)
		_ = c.deleteWorkflow(ctx, id)

		for _, p := range gotRawPaths {
			// After PathEscape, "../" becomes "..%2F" and spaces become "%20".
			if strings.Contains(p, "../") || strings.Contains(p, " ") {
				t.Errorf("path not properly escaped: %s (from id %q)", p, id)
			}
		}
	}
}

func TestBuildDeletePatch(t *testing.T) {
	patch := map[string]any{
		"topics": map[string]any{"job.pack1.ok": map[string]any{"timeout": 10}},
		"pools":  map[string]any{"pack1.pool": map[string]any{"requires": []any{"gpu"}}},
	}
	out := buildDeletePatch(patch)
	topics, ok := out["topics"].(map[string]any)
	if !ok || topics["job.pack1.ok"] == nil {
		t.Fatalf("expected delete patch for topics")
	}
}

func TestAcquirePackLocks_RetriesGlobalReleaseOnPackLockFailure(t *testing.T) {
	var globalReleaseCalls int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Resource string `json:"resource"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/locks/acquire" && req.Resource == "packs:global":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/locks/acquire" && req.Resource == "pack:demo-pack":
			http.Error(w, "pack lock unavailable", http.StatusConflict)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/locks/release" && req.Resource == "packs:global":
			globalReleaseCalls++
			if globalReleaseCalls == 1 {
				http.Error(w, "transient global release failure", http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request: %s %s resource=%q", r.Method, r.URL.Path, req.Resource)
		}
	}))
	defer srv.Close()

	client := &restClient{baseURL: srv.URL, httpClient: srv.Client()}
	release, err := acquirePackLocks(context.Background(), client, "demo-pack", "owner-1")
	if err == nil {
		t.Fatal("expected pack lock acquisition error")
	}
	if globalReleaseCalls != 1 {
		t.Fatalf("expected one immediate global release attempt, got %d", globalReleaseCalls)
	}

	release()
	if globalReleaseCalls != 2 {
		t.Fatalf("expected cleanup to retry global release, got %d calls", globalReleaseCalls)
	}

	release()
	if globalReleaseCalls != 2 {
		t.Fatalf("expected cleanup to become a no-op after successful release, got %d calls", globalReleaseCalls)
	}
}

func TestRunPackInstallReleasesLocksOnError(t *testing.T) {
	packDir := t.TempDir()
	manifest := fmt.Sprintf(`
apiVersion: cordum.io/v1
kind: Pack
metadata:
  id: demo-pack
  version: 1.0.0
compatibility:
  protocolVersion: %d
`, capsdk.DefaultProtocolVersion)
	if err := os.WriteFile(filepath.Join(packDir, "pack.yaml"), []byte(strings.TrimSpace(manifest)), 0o600); err != nil {
		t.Fatalf("write pack manifest: %v", err)
	}

	var releaseCalls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/locks/acquire":
			var req struct {
				Resource string `json:"resource"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode acquire lock request: %v", err)
			}
			switch req.Resource {
			case "packs:global", "pack:demo-pack":
				w.WriteHeader(http.StatusNoContent)
			default:
				t.Fatalf("unexpected acquire lock resource: %q", req.Resource)
			}
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/locks/release":
			var req struct {
				Resource string `json:"resource"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode release lock request: %v", err)
			}
			releaseCalls = append(releaseCalls, req.Resource)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/config":
			if got := r.URL.Query().Get("scope"); got != packRegistryScope {
				t.Fatalf("unexpected config scope: %q", got)
			}
			if got := r.URL.Query().Get("scope_id"); got != packRegistryID {
				t.Fatalf("unexpected config scope_id: %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"scope":"system","scope_id":"packs","data":{}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/config":
			http.Error(w, "simulated registry failure", http.StatusInternalServerError)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()

	err := runPackInstall([]string{"--gateway", srv.URL, "--force", packDir})
	if err == nil {
		t.Fatal("expected runPackInstall to return an error")
	}
	if !strings.Contains(err.Error(), "update pack registry") {
		t.Fatalf("expected updatePackRegistry error, got: %v", err)
	}

	wantReleaseCalls := []string{"pack:demo-pack", "packs:global"}
	if !reflect.DeepEqual(releaseCalls, wantReleaseCalls) {
		t.Fatalf("unexpected release calls\n got: %v\nwant: %v", releaseCalls, wantReleaseCalls)
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
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/locks/acquire":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/locks/release":
			w.WriteHeader(http.StatusNoContent)
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

func TestRunPackUninstallReleasesLocksOnError(t *testing.T) {
	var releaseCalls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/locks/acquire":
			var req struct {
				Resource string `json:"resource"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode acquire lock request: %v", err)
			}
			switch req.Resource {
			case "packs:global", "pack:demo-pack":
				w.WriteHeader(http.StatusNoContent)
			default:
				t.Fatalf("unexpected acquire lock resource: %q", req.Resource)
			}
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/locks/release":
			var req struct {
				Resource string `json:"resource"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode release lock request: %v", err)
			}
			releaseCalls = append(releaseCalls, req.Resource)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/config":
			if got := r.URL.Query().Get("scope"); got != packRegistryScope {
				t.Fatalf("unexpected config scope: %q", got)
			}
			if got := r.URL.Query().Get("scope_id"); got != packRegistryID {
				t.Fatalf("unexpected config scope_id: %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"scope":"system","scope_id":"packs","data":{"installed":{"demo-pack":{"id":"demo-pack","version":"1.0.0","status":"ACTIVE","overlays":{"config":[],"policy":[]},"resources":{"schemas":{},"workflows":{}}}}}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/config":
			http.Error(w, "simulated registry failure", http.StatusInternalServerError)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()

	err := runPackUninstall([]string{"--gateway", srv.URL, "demo-pack"})
	if err == nil {
		t.Fatal("expected runPackUninstall to return an error")
	}
	if !strings.Contains(err.Error(), "update pack registry") {
		t.Fatalf("expected updatePackRegistry error, got: %v", err)
	}

	wantReleaseCalls := []string{"pack:demo-pack", "packs:global"}
	if !reflect.DeepEqual(releaseCalls, wantReleaseCalls) {
		t.Fatalf("unexpected release calls\n got: %v\nwant: %v", releaseCalls, wantReleaseCalls)
	}
}

func TestRollbackPackInstallWithHooks_ReversesInstallOrderPerStage(t *testing.T) {
	tests := []struct {
		name      string
		policies  []appliedPolicyChange
		configs   []appliedConfigChange
		workflows []workflowPlan
		schemas   []schemaPlan
		want      []string
	}{
		{
			name: "schema stage failure rolls back schemas only",
			schemas: []schemaPlan{
				{ID: "schema.one"},
				{ID: "schema.two"},
			},
			want: []string{
				"schema:schema.two",
				"schema:schema.one",
			},
		},
		{
			name: "workflow stage failure rolls back workflows before schemas",
			workflows: []workflowPlan{
				{ID: "workflow.one"},
				{ID: "workflow.two"},
			},
			schemas: []schemaPlan{
				{ID: "schema.one"},
			},
			want: []string{
				"workflow:workflow.two",
				"workflow:workflow.one",
				"schema:schema.one",
			},
		},
		{
			name: "config stage failure rolls back config overlays before workflows and schemas",
			configs: []appliedConfigChange{
				{Overlay: packAppliedConfigOverlay{Name: "config.one"}},
				{Overlay: packAppliedConfigOverlay{Name: "config.two"}},
			},
			workflows: []workflowPlan{
				{ID: "workflow.one"},
			},
			schemas: []schemaPlan{
				{ID: "schema.one"},
			},
			want: []string{
				"config:config.two",
				"config:config.one",
				"workflow:workflow.one",
				"schema:schema.one",
			},
		},
		{
			name: "policy stage failure rolls back policies before configs workflows and schemas",
			policies: []appliedPolicyChange{
				{Overlay: packAppliedPolicyOverlay{Name: "policy.one", FragmentID: "pack/policy.one"}},
				{Overlay: packAppliedPolicyOverlay{Name: "policy.two", FragmentID: "pack/policy.two"}},
			},
			configs: []appliedConfigChange{
				{Overlay: packAppliedConfigOverlay{Name: "config.one"}},
			},
			workflows: []workflowPlan{
				{ID: "workflow.one"},
			},
			schemas: []schemaPlan{
				{ID: "schema.one"},
			},
			want: []string{
				"policy:policy.two",
				"policy:policy.one",
				"config:config.one",
				"workflow:workflow.one",
				"schema:schema.one",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			hooks := packInstallRollbackHooks{
				restorePolicyOverlay: func(_ context.Context, _ *restClient, change appliedPolicyChange) error {
					got = append(got, "policy:"+change.Overlay.Name)
					return nil
				},
				restoreConfigOverlay: func(_ context.Context, _ *restClient, change appliedConfigChange) error {
					got = append(got, "config:"+change.Overlay.Name)
					return nil
				},
				rollbackWorkflow: func(_ context.Context, _ *restClient, plan workflowPlan) error {
					got = append(got, "workflow:"+plan.ID)
					return nil
				},
				rollbackSchema: func(_ context.Context, _ *restClient, plan schemaPlan) error {
					got = append(got, "schema:"+plan.ID)
					return nil
				},
			}

			rollbackPackInstallWithHooks(
				context.Background(),
				nil,
				tt.policies,
				tt.configs,
				tt.workflows,
				tt.schemas,
				hooks,
			)

			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("rollback order mismatch\n got: %v\nwant: %v", got, tt.want)
			}
		})
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

func TestUpdatePackRegistryHandlesNilConfigData(t *testing.T) {
	var posted map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/config":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"scope":"system","scope_id":"packs","data":null}`))
			return
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/config":
			if err := json.NewDecoder(r.Body).Decode(&posted); err != nil {
				t.Fatalf("decode posted config: %v", err)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
			return
		default:
			http.NotFound(w, r)
			return
		}
	}))
	defer srv.Close()

	client := &restClient{baseURL: srv.URL, httpClient: srv.Client()}
	err := updatePackRegistry(context.Background(), client, packRecord{
		ID:      "pack.demo",
		Version: "1.0.0",
		Status:  "ACTIVE",
	})
	if err != nil {
		t.Fatalf("updatePackRegistry returned error: %v", err)
	}

	if posted == nil {
		t.Fatalf("expected setConfig POST body")
	}
	data, ok := posted["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected data map in POST body, got %T", posted["data"])
	}
	installed, ok := data["installed"].(map[string]any)
	if !ok {
		t.Fatalf("expected installed map in data, got %T", data["installed"])
	}
	record, ok := installed["pack.demo"].(map[string]any)
	if !ok {
		t.Fatalf("expected posted record for pack.demo, got %T", installed["pack.demo"])
	}
	if got := record["id"]; got != "pack.demo" {
		t.Fatalf("expected posted id pack.demo, got %v", got)
	}
}
