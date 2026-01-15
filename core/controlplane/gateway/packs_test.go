package gateway

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/configsvc"
)

func installTestPack(t *testing.T, s *server) {
	t.Helper()
	if err := s.configSvc.Set(context.Background(), &configsvc.Document{
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
  id: test-pack
  version: 0.1.0
  title: Test Pack
compatibility:
  protocolVersion: 1
topics:
  - name: job.test-pack.collect
resources:
  schemas:
    - id: test-pack/Incident
      path: schemas/Incident.json
  workflows:
    - id: test-pack.triage
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
tests:
  policySimulations:
    - name: simulate
      request:
        tenantId: default
        topic: job.test-pack.collect
`,
		"schemas/Incident.json": `{"type":"object","properties":{"message":{"type":"string"}}}`,
		"workflows/triage.yaml": `
id: test-pack.triage
org_id: default
name: Triage
steps:
  approve:
    type: approval
`,
		"overlays/pools.patch.yaml": `
topics:
  job.test-pack.collect: ["test-pack"]
pools:
  test-pack:
    requires: []
`,
		"overlays/policy.fragment.yaml": `
tenants:
  default:
    allow_topics:
      - job.test-pack.*
`,
	}
	bundle := buildTarGz(t, files)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("bundle", "test-pack.tgz")
	if err != nil {
		t.Fatalf("form file: %v", err)
	}
	if _, err := part.Write(bundle); err != nil {
		t.Fatalf("write bundle: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/packs/install", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rr := httptest.NewRecorder()
	s.handleInstallPack(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleInstallPack(t *testing.T) {
	s, _, _ := newTestGateway(t)
	installTestPack(t, s)

	ctx := context.Background()
	if _, err := s.schemaRegistry.Get(ctx, "test-pack/Incident"); err != nil {
		t.Fatalf("schema not registered: %v", err)
	}
	if _, err := s.workflowStore.GetWorkflow(ctx, "test-pack.triage"); err != nil {
		t.Fatalf("workflow not registered: %v", err)
	}
	doc, err := s.configSvc.Get(ctx, "system", "default")
	if err != nil {
		t.Fatalf("config get: %v", err)
	}
	poolsRaw := doc.Data["pools"]
	if poolsRaw == nil {
		t.Fatalf("expected pools config")
	}
	policyDoc, err := s.configSvc.Get(ctx, "system", "policy")
	if err != nil {
		t.Fatalf("policy doc missing: %v", err)
	}
	bundles, _ := policyDoc.Data["bundles"].(map[string]any)
	if bundles == nil || bundles["test-pack/safety"] == nil {
		t.Fatalf("policy bundle not installed")
	}
}

func TestHandleListAndGetPacks(t *testing.T) {
	s, _, _ := newTestGateway(t)
	installTestPack(t, s)

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/packs", nil)
	listRR := httptest.NewRecorder()
	s.handleListPacks(listRR, listReq)
	if listRR.Code != http.StatusOK {
		t.Fatalf("list packs: %d %s", listRR.Code, listRR.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(listRR.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	items, _ := payload["items"].([]any)
	if len(items) == 0 {
		t.Fatalf("expected pack list")
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/packs/test-pack", nil)
	getReq.SetPathValue("id", "test-pack")
	getRR := httptest.NewRecorder()
	s.handleGetPack(getRR, getReq)
	if getRR.Code != http.StatusOK {
		t.Fatalf("get pack: %d %s", getRR.Code, getRR.Body.String())
	}
}

func TestHandleVerifyAndUninstallPack(t *testing.T) {
	s, _, _ := newTestGateway(t)
	installTestPack(t, s)

	verifyReq := httptest.NewRequest(http.MethodPost, "/api/v1/packs/test-pack/verify", nil)
	verifyReq.SetPathValue("id", "test-pack")
	verifyRR := httptest.NewRecorder()
	s.handleVerifyPack(verifyRR, verifyReq)
	if verifyRR.Code != http.StatusOK {
		t.Fatalf("verify pack: %d %s", verifyRR.Code, verifyRR.Body.String())
	}

	uninstallReq := httptest.NewRequest(http.MethodPost, "/api/v1/packs/test-pack/uninstall", nil)
	uninstallReq.SetPathValue("id", "test-pack")
	uninstallRR := httptest.NewRecorder()
	s.handleUninstallPack(uninstallRR, uninstallReq)
	if uninstallRR.Code != http.StatusOK {
		t.Fatalf("uninstall pack: %d %s", uninstallRR.Code, uninstallRR.Body.String())
	}
	var rec map[string]any
	if err := json.Unmarshal(uninstallRR.Body.Bytes(), &rec); err != nil {
		t.Fatalf("decode uninstall response: %v", err)
	}
	if rec["status"] != "DISABLED" {
		t.Fatalf("expected disabled status")
	}
}

func buildTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	now := time.Now()
	for name, content := range files {
		clean := filepath.ToSlash(strings.TrimPrefix(name, "/"))
		if clean == "" {
			continue
		}
		hdr := &tar.Header{
			Name:    clean,
			Mode:    0o644,
			Size:    int64(len(content)),
			ModTime: now,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write header: %v", err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("write content: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}
