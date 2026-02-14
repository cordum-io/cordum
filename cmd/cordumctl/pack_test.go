package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
