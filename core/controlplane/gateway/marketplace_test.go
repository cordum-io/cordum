package gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/configsvc"
)

func TestMarketplacePacks(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()

	if err := s.updatePackRegistry(ctx, packRecord{
		ID:      "demo-pack",
		Version: "0.9.0",
		Status:  "ACTIVE",
	}); err != nil {
		t.Fatalf("seed pack registry: %v", err)
	}

	packBytes := buildTarGz(t, map[string]string{
		"pack.yaml": `
apiVersion: cordum.io/v1alpha1
kind: Pack
metadata:
  id: demo-pack
  version: 1.0.0
compatibility:
  protocolVersion: 1
resources:
  schemas:
    - id: demo-pack/Incident
      path: schemas/Incident.json
  workflows:
    - id: demo-pack.triage
      path: workflows/triage.yaml
`,
		"schemas/Incident.json": `{"type":"object","properties":{"message":{"type":"string"}}}`,
		"workflows/triage.yaml": `
id: demo-pack.triage
org_id: default
name: Demo
steps:
  approve:
    type: approval
`,
	})
	sum := sha256.Sum256(packBytes)
	catalog := marketplaceCatalogFile{
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		Packs: []marketplaceCatalogPack{
			{
				ID:          "demo-pack",
				Version:     "1.0.0",
				Title:       "Demo Pack",
				Description: "Marketplace demo",
				Image:       "https://example.com/demo.png",
				URL:         "http://invalid.local/demo-pack.tgz",
				Sha256:      hex.EncodeToString(sum[:]),
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "catalog.json") {
			w.Header().Set("Content-Type", "application/json")
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
					"id":      "official",
					"title":   "Official",
					"url":     server.URL + "/catalog.json",
					"enabled": true,
				},
			},
		},
	}); err != nil {
		t.Fatalf("seed pack catalogs: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/marketplace/packs", nil)
	req.Header.Set("X-Tenant-ID", "default")
	rr := httptest.NewRecorder()
	s.handleMarketplacePacks(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d body=%s", rr.Code, rr.Body.String())
	}

	var resp marketplaceResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 pack, got %d", len(resp.Items))
	}
	if resp.Items[0].InstalledVersion != "0.9.0" {
		t.Fatalf("expected installed version, got %s", resp.Items[0].InstalledVersion)
	}
	if resp.Items[0].Image != "https://example.com/demo.png" {
		t.Fatalf("expected image, got %s", resp.Items[0].Image)
	}
}

func TestMarketplaceInstallFromCatalog(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()

	if err := s.configSvc.Set(ctx, &configsvc.Document{
		Scope:   configsvc.ScopeSystem,
		ScopeID: "default",
		Data:    map[string]any{"pools": map[string]any{}},
	}); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	files := map[string]string{
		"pack.yaml": `
apiVersion: cordum.io/v1alpha1
kind: Pack
metadata:
  id: demo-pack
  version: 0.1.0
compatibility:
  protocolVersion: 1
topics:
  - name: job.demo-pack.collect
resources:
  schemas:
    - id: demo-pack/Incident
      path: schemas/Incident.json
  workflows:
    - id: demo-pack.triage
      path: workflows/triage.yaml
overlays:
  config:
    - name: pools
      scope: system
      key: pools
      strategy: json_merge_patch
      path: overlays/pools.patch.yaml
  policy:
    - name: safety
      strategy: bundle_fragment
      path: overlays/policy.fragment.yaml
`,
		"schemas/Incident.json": `{"type":"object","properties":{"message":{"type":"string"}}}`,
		"workflows/triage.yaml": `
id: demo-pack.triage
org_id: default
name: Demo
steps:
  approve:
    type: approval
`,
		"overlays/pools.patch.yaml": `
topics:
  job.demo-pack.collect: ["demo-pack"]
pools:
  demo-pack:
    requires: []
`,
		"overlays/policy.fragment.yaml": `
tenants:
  default:
    allow_topics:
      - job.demo-pack.*
`,
	}
	bundle := buildTarGz(t, files)
	sum := sha256.Sum256(bundle)
	catalog := marketplaceCatalogFile{
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		Packs: []marketplaceCatalogPack{
			{
				ID:          "demo-pack",
				Version:     "0.1.0",
				Title:       "Demo Pack",
				Description: "Marketplace install demo",
				URL:         "",
				Sha256:      hex.EncodeToString(sum[:]),
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/catalog.json":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(catalog)
		case "/demo-pack.tgz":
			w.Header().Set("Content-Type", "application/gzip")
			_, _ = w.Write(bundle)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	catalog.Packs[0].URL = server.URL + "/demo-pack.tgz"
	if err := s.configSvc.Set(ctx, &configsvc.Document{
		Scope:   configsvc.ScopeSystem,
		ScopeID: packCatalogID,
		Data: map[string]any{
			"catalogs": []any{
				map[string]any{
					"id":      "official",
					"title":   "Official",
					"url":     server.URL + "/catalog.json",
					"enabled": true,
				},
			},
		},
	}); err != nil {
		t.Fatalf("seed pack catalogs: %v", err)
	}

	payload := map[string]any{
		"catalog_id": "official",
		"pack_id":    "demo-pack",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/marketplace/install", bytes.NewReader(body))
	req.Header.Set("X-Tenant-ID", "default")
	rr := httptest.NewRecorder()
	s.handleMarketplaceInstall(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d body=%s", rr.Code, rr.Body.String())
	}
	if _, err := s.schemaRegistry.Get(ctx, "demo-pack/Incident"); err != nil {
		t.Fatalf("schema not registered: %v", err)
	}
	if _, err := s.workflowStore.GetWorkflow(ctx, "demo-pack.triage"); err != nil {
		t.Fatalf("workflow not registered: %v", err)
	}
	policyDoc, err := s.configSvc.Get(ctx, "system", "policy")
	if err != nil {
		t.Fatalf("policy doc missing: %v", err)
	}
	bundles, _ := policyDoc.Data["bundles"].(map[string]any)
	if bundles == nil || bundles["demo-pack/safety"] == nil {
		t.Fatalf("policy bundle not installed")
	}
}

func TestValidateMarketplaceURLRejectsPrivateResolution(t *testing.T) {
	prevLookup := lookupHostIPs
	prevSkip := skipPrivateIPCheck.Load()
	t.Cleanup(func() {
		lookupHostIPs = prevLookup
		skipPrivateIPCheck.Store(prevSkip)
	})

	skipPrivateIPCheck.Store(false)
	lookupHostIPs = func(ctx context.Context, host string) ([]net.IP, error) {
		if host == "example.com" {
			return []net.IP{net.ParseIP("10.0.0.1")}, nil
		}
		return nil, errors.New("unexpected host")
	}

	allowed := map[string]struct{}{"example.com": {}}
	if _, err := validateMarketplaceURL("https://example.com/catalog.json", allowed); err == nil {
		t.Fatalf("expected private resolution to be rejected")
	}
}

func TestValidateMarketplaceURLAllowsPublicResolution(t *testing.T) {
	prevLookup := lookupHostIPs
	prevSkip := skipPrivateIPCheck.Load()
	t.Cleanup(func() {
		lookupHostIPs = prevLookup
		skipPrivateIPCheck.Store(prevSkip)
	})

	skipPrivateIPCheck.Store(false)
	lookupHostIPs = func(ctx context.Context, host string) ([]net.IP, error) {
		if host == "example.com" {
			return []net.IP{net.ParseIP("93.184.216.34")}, nil
		}
		return nil, errors.New("unexpected host")
	}

	allowed := map[string]struct{}{"example.com": {}}
	if _, err := validateMarketplaceURL("https://example.com/catalog.json", allowed); err != nil {
		t.Fatalf("expected public resolution to pass: %v", err)
	}
}

func TestMarketplaceRedirectValidationBlocksCrossHostAndPrivate(t *testing.T) {
	prevLookup := lookupHostIPs
	prevSkip := skipPrivateIPCheck.Load()
	t.Cleanup(func() {
		lookupHostIPs = prevLookup
		skipPrivateIPCheck.Store(prevSkip)
	})

	skipPrivateIPCheck.Store(false)
	lookupHostIPs = func(ctx context.Context, host string) ([]net.IP, error) {
		switch host {
		case "example.com", "evil.example":
			return []net.IP{net.ParseIP("93.184.216.34")}, nil
		case "private.example":
			return []net.IP{net.ParseIP("10.0.0.2")}, nil
		default:
			return nil, errors.New("unexpected host")
		}
	}

	allowed := map[string]struct{}{
		"example.com":     {},
		"private.example": {},
	}
	client := marketplaceHTTPClient(allowed, "example.com")
	baseReq, _ := http.NewRequest(http.MethodGet, "https://example.com/start", nil)

	evilReq, _ := http.NewRequest(http.MethodGet, "https://evil.example/next", nil)
	if err := client.CheckRedirect(evilReq, []*http.Request{baseReq}); err == nil {
		t.Fatalf("expected cross-host redirect to be blocked")
	}

	privateReq, _ := http.NewRequest(http.MethodGet, "https://private.example/next", nil)
	if err := client.CheckRedirect(privateReq, []*http.Request{baseReq}); err == nil {
		t.Fatalf("expected private redirect to be blocked")
	}
}

func TestMarketplaceDialContextRejectsPrivateIP(t *testing.T) {
	prevLookup := lookupHostIPs
	prevSkip := skipPrivateIPCheck.Load()
	t.Cleanup(func() {
		lookupHostIPs = prevLookup
		skipPrivateIPCheck.Store(prevSkip)
	})

	skipPrivateIPCheck.Store(false)
	lookupHostIPs = func(ctx context.Context, host string) ([]net.IP, error) {
		if host == "private.example" {
			return []net.IP{net.ParseIP("10.0.0.3")}, nil
		}
		return nil, errors.New("unexpected host")
	}

	allowed := map[string]struct{}{"private.example": {}}
	dial := marketplaceDialContext(allowed)
	if _, err := dial(context.Background(), "tcp", "private.example:443"); err == nil {
		t.Fatalf("expected dial to reject private IP")
	}
}
