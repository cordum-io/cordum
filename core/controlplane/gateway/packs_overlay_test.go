package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cordum/cordum/core/configsvc"
	wf "github.com/cordum/cordum/core/workflow"
	"github.com/redis/go-redis/v9"
)

func TestCompareAndParseVersions(t *testing.T) {
	if compareVersions("1.2.0", "1.10.0") != -1 {
		t.Fatalf("expected 1.2.0 < 1.10.0")
	}
	if compareVersions("2.0", "1.9") != 1 {
		t.Fatalf("expected 2.0 > 1.9")
	}
	if compareVersions("v1.0.0", "1.0.0") != 0 {
		t.Fatalf("expected version normalization")
	}
	if _, ok := parseVersion("1.2.3-beta"); ok {
		t.Fatalf("expected prerelease to be invalid")
	}
	if parts, ok := parseVersion("v1.2.3"); !ok || len(parts) != 3 || parts[0] != 1 {
		t.Fatalf("expected parsed version parts")
	}
}

func TestFindMarketplaceEntryByURL(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()

	catalog := marketplaceCatalogFile{
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		Packs: []marketplaceCatalogPack{{
			ID:      "demo-pack",
			Version: "1.0.0",
			Title:   "Demo",
			URL:     "",
		}},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/catalog.json" {
			_ = json.NewEncoder(w).Encode(catalog)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	catalog.Packs[0].URL = server.URL + "/demo-pack.tgz"
	if err := s.configSvc.Set(ctx, &configsvc.Document{
		Scope:   configsvc.ScopeSystem,
		ScopeID: packCatalogID,
		Data: map[string]any{
			"catalogs": []any{
				map[string]any{
					"id":      "test",
					"title":   "Test",
					"url":     server.URL + "/catalog.json",
					"enabled": true,
				},
			},
		},
	}); err != nil {
		t.Fatalf("seed pack catalogs: %v", err)
	}

	entry, err := s.findMarketplaceEntryByURL(ctx, catalog.Packs[0].URL)
	if err != nil {
		t.Fatalf("find marketplace entry: %v", err)
	}
	if entry.Pack.ID != "demo-pack" {
		t.Fatalf("unexpected pack id: %s", entry.Pack.ID)
	}
}

func TestConfigAndPolicyOverlayRollback(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()

	configDoc := &configsvc.Document{
		Scope:   configsvc.ScopeSystem,
		ScopeID: "default",
		Data: map[string]any{
			"pools": map[string]any{
				"topics": map[string]any{
					"job.pack.test": []any{"pack"},
				},
				"pools": map[string]any{
					"pack": map[string]any{"requires": []any{}},
				},
			},
		},
	}
	if err := s.configSvc.Set(ctx, configDoc); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	overlay := packAppliedConfigOverlay{
		Name:    "pools",
		Scope:   "system",
		ScopeID: "default",
		Key:     "pools",
		Patch: map[string]any{
			"topics": map[string]any{"job.pack.test": []any{"pack"}},
			"pools":  map[string]any{"pack": map[string]any{"requires": []any{}}},
		},
	}
	if err := s.removeConfigOverlay(ctx, overlay); err != nil {
		t.Fatalf("remove config overlay: %v", err)
	}
	doc, err := s.configSvc.Get(ctx, configsvc.ScopeSystem, "default")
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	if pools, ok := doc.Data["pools"].(map[string]any); ok {
		if topics, ok := pools["topics"].(map[string]any); ok {
			if _, ok := topics["job.pack.test"]; ok {
				t.Fatalf("expected topic removed")
			}
		}
	}

	change := appliedConfigChange{Overlay: overlay, Previous: configDoc.Data["pools"]}
	if err := s.restoreConfigOverlay(ctx, change); err != nil {
		t.Fatalf("restore config overlay: %v", err)
	}
	doc, err = s.configSvc.Get(ctx, configsvc.ScopeSystem, "default")
	if err != nil {
		t.Fatalf("get config after restore: %v", err)
	}
	if pools, ok := doc.Data["pools"].(map[string]any); ok {
		if topics, ok := pools["topics"].(map[string]any); ok {
			if _, ok := topics["job.pack.test"]; !ok {
				t.Fatalf("expected topic restored")
			}
		}
	}

	fragmentID := policyFragmentID("pack", "default")
	policyDoc := &configsvc.Document{
		Scope:   configsvc.ScopeSystem,
		ScopeID: policyConfigID,
		Data: map[string]any{
			policyConfigKey: map[string]any{
				fragmentID: map[string]any{"content": "policy"},
			},
		},
	}
	if err := s.configSvc.Set(ctx, policyDoc); err != nil {
		t.Fatalf("seed policy doc: %v", err)
	}
	policyOverlay := packAppliedPolicyOverlay{Name: "default", FragmentID: fragmentID}
	if err := s.removePolicyOverlay(ctx, policyOverlay); err != nil {
		t.Fatalf("remove policy overlay: %v", err)
	}
	doc, err = s.configSvc.Get(ctx, configsvc.ScopeSystem, policyConfigID)
	if err != nil {
		t.Fatalf("get policy doc: %v", err)
	}
	if bundles, ok := doc.Data[policyConfigKey].(map[string]any); ok {
		if _, ok := bundles[fragmentID]; ok {
			t.Fatalf("expected policy fragment removed")
		}
	}
	policyChange := appliedPolicyChange{Overlay: policyOverlay, Previous: map[string]any{"content": "policy"}, HadPrevious: true}
	if err := s.restorePolicyOverlay(ctx, policyChange); err != nil {
		t.Fatalf("restore policy overlay: %v", err)
	}
}

func TestRollbackSchemaAndWorkflow(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()

	schemaID := "pack/schema"
	data := []byte(`{"type":"object","properties":{"a":{"type":"string"}}}`)
	if err := s.schemaRegistry.Register(ctx, schemaID, data); err != nil {
		t.Fatalf("set schema: %v", err)
	}
	var schemaMap map[string]any
	if err := json.Unmarshal(data, &schemaMap); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	plan := schemaPlan{ID: schemaID, HadExisting: true, Existing: schemaMap}
	if err := s.rollbackSchema(ctx, plan); err != nil {
		t.Fatalf("rollback schema: %v", err)
	}

	wfDef := &wf.Workflow{ID: "wf-rollback", OrgID: "default", Steps: map[string]*wf.Step{}}
	if err := s.workflowStore.SaveWorkflow(ctx, wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	if err := s.rollbackWorkflow(ctx, workflowPlan{ID: wfDef.ID, HadExisting: true, Existing: workflowToMap(wfDef)}); err != nil {
		t.Fatalf("rollback workflow: %v", err)
	}

	if err := s.rollbackSchema(ctx, schemaPlan{ID: "pack/schema2"}); err != nil {
		t.Fatalf("rollback schema delete: %v", err)
	}
	if err := s.rollbackWorkflow(ctx, workflowPlan{ID: "wf-delete"}); err != nil && !errors.Is(err, redis.Nil) {
		t.Fatalf("rollback workflow delete: %v", err)
	}
}

func TestRunPolicySimulation(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.WithValue(context.Background(), authContextKey{}, &AuthContext{Tenant: "override", PrincipalID: "actor-1"})

	test := packPolicySimulation{Name: "sim", Request: packPolicySimulationRequest{Topic: "job.default"}}
	decision, reason, err := s.runPolicySimulation(ctx, test, "pack")
	if err != nil {
		t.Fatalf("run policy simulation: %v", err)
	}
	if decision == "" || reason == "" {
		t.Fatalf("expected decision and reason")
	}

	_, _, err = s.runPolicySimulation(ctx, packPolicySimulation{Name: "bad"}, "pack")
	if err == nil {
		t.Fatalf("expected error for missing topic")
	}
}

func TestDownloadPackBundleErrors(t *testing.T) {
	if _, _, _, err := downloadPackBundle(context.Background(), nil); err == nil {
		t.Fatalf("expected error for nil url")
	}
}

func TestHashWorkflow(t *testing.T) {
	wfMap := map[string]any{"id": "wf", "steps": map[string]any{}}
	hash, err := hashWorkflow(wfMap)
	if err != nil || hash == "" {
		t.Fatalf("expected hash for workflow")
	}
}
