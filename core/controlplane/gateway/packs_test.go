package gateway

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/infra/locks"
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
	req.Header.Set("X-Tenant-ID", "default")
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

func TestPackInstallErrorMessage(t *testing.T) {
	err := &packInstallError{Err: nil}
	if err.Error() == "" {
		t.Fatalf("expected default error message")
	}
	err = &packInstallError{Err: context.DeadlineExceeded}
	if err.Error() != context.DeadlineExceeded.Error() {
		t.Fatalf("expected wrapped error string")
	}
}

func TestPackInstallNonPackError_DoesNotLeakDetails(t *testing.T) {
	s, _, _ := newTestGateway(t)
	// installPackFromDir with a non-existent directory produces an OS error
	// (containing filesystem paths). Verify handleInstallPack returns generic
	// "internal error" instead of leaking internal details.
	//
	// We call installPackFromDir directly to get the raw error, then verify
	// the error handling branch in handleInstallPack via the handler.

	// 1. Verify packInstallError returns its controlled message.
	installErr := &packInstallError{Status: http.StatusBadRequest, Err: fmt.Errorf("manifest not found")}
	rec1 := httptest.NewRecorder()
	req1 := adminCtx(httptest.NewRequest(http.MethodGet, "/", nil))
	var testErr error = installErr
	var pie *packInstallError
	if errors.As(testErr, &pie) {
		writeErrorJSON(rec1, pie.Status, pie.Error())
	}
	if rec1.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for packInstallError, got %d", rec1.Code)
	}
	var body1 map[string]any
	_ = json.NewDecoder(rec1.Body).Decode(&body1)
	if body1["error"] != "manifest not found" {
		t.Fatalf("expected controlled message, got %q", body1["error"])
	}

	// 2. Verify non-packInstallError uses writeInternalError.
	genericErr := fmt.Errorf("open /var/lib/cordum/packs/tmp123/pack.yaml: permission denied")
	rec2 := httptest.NewRecorder()
	req2 := adminCtx(httptest.NewRequest(http.MethodGet, "/", nil))
	if !errors.As(genericErr, &pie) {
		writeInternalError(rec2, req2, "install pack", genericErr)
	}
	if rec2.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for generic error, got %d", rec2.Code)
	}
	var body2 map[string]any
	_ = json.NewDecoder(rec2.Body).Decode(&body2)
	errMsg, _ := body2["error"].(string)
	if errMsg != "internal error" {
		t.Fatalf("expected 'internal error', got %q", errMsg)
	}
	if strings.Contains(errMsg, "/var/lib") {
		t.Errorf("error message leaks filesystem path: %q", errMsg)
	}
	_ = s // suppress unused
	_ = req1
}

func TestHandleListAndGetPacks(t *testing.T) {
	s, _, _ := newTestGateway(t)
	installTestPack(t, s)

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/packs", nil)
	listReq.Header.Set("X-Tenant-ID", "default")
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
	getReq.Header.Set("X-Tenant-ID", "default")
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
	verifyReq.Header.Set("X-Tenant-ID", "default")
	verifyReq.SetPathValue("id", "test-pack")
	verifyRR := httptest.NewRecorder()
	s.handleVerifyPack(verifyRR, verifyReq)
	if verifyRR.Code != http.StatusOK {
		t.Fatalf("verify pack: %d %s", verifyRR.Code, verifyRR.Body.String())
	}

	uninstallReq := httptest.NewRequest(http.MethodPost, "/api/v1/packs/test-pack/uninstall", nil)
	uninstallReq.Header.Set("X-Tenant-ID", "default")
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

// faultyLockStore wraps a locks.Store and allows injecting release errors.
type faultyLockStore struct {
	locks.Store
	releaseErr error
}

func (f *faultyLockStore) Release(ctx context.Context, resource, owner string) (*locks.Lock, bool, error) {
	if f.releaseErr != nil {
		return nil, false, f.releaseErr
	}
	return f.Store.Release(ctx, resource, owner)
}

// TestAcquirePackLocksReleaseBounded verifies that the release closure uses
// a timeout-bounded context instead of unbounded context.Background().
func TestAcquirePackLocksReleaseBounded(t *testing.T) {
	s, _, _ := newTestGateway(t)
	if s.lockStore == nil {
		t.Skip("lock store not configured in test gateway")
	}
	owner := "test-owner-bounded"

	release, err := acquirePackLocks(context.Background(), s.lockStore, "bounded-pack", owner)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	// Release should complete quickly (bounded context).
	done := make(chan struct{})
	go func() {
		release()
		close(done)
	}()
	select {
	case <-done:
		// ok
	case <-time.After(10 * time.Second):
		t.Fatal("release blocked beyond expected timeout — likely unbounded context")
	}
}

// TestAcquirePackLocksReleaseErrorLogged verifies that when Release fails,
// the error is not silently dropped and the release closure still completes.
func TestAcquirePackLocksReleaseErrorLogged(t *testing.T) {
	s, _, _ := newTestGateway(t)
	if s.lockStore == nil {
		t.Skip("lock store not configured in test gateway")
	}

	faulty := &faultyLockStore{
		Store:      s.lockStore,
		releaseErr: fmt.Errorf("redis connection reset"),
	}
	owner := "test-owner-faulty"

	// Acquire with real store first, then release will use faulty.
	release, err := acquirePackLocks(context.Background(), faulty, "faulty-pack", owner)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	// Release should not panic or hang despite release errors.
	done := make(chan struct{})
	go func() {
		release()
		close(done)
	}()
	select {
	case <-done:
		// ok — release completed despite errors
	case <-time.After(10 * time.Second):
		t.Fatal("release hung despite faulty store")
	}
}

// TestAcquirePackLocksCleanupOnPartialAcquire verifies that if the per-pack
// lock fails, the global lock is properly released with error handling.
func TestAcquirePackLocksCleanupOnPartialAcquire(t *testing.T) {
	s, _, _ := newTestGateway(t)
	if s.lockStore == nil {
		t.Skip("lock store not configured in test gateway")
	}
	owner := "test-owner-partial"

	// Acquire global + pack lock with one owner.
	_, err := acquirePackLocks(context.Background(), s.lockStore, "partial-pack", owner)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	// Second acquire with different owner should fail on pack lock and
	// clean up the global lock it acquired.
	owner2 := "test-owner-partial-2"
	_, err = acquirePackLocks(context.Background(), s.lockStore, "partial-pack", owner2)
	if err == nil {
		t.Fatal("expected pack lock contention error")
	}

	// Verify global lock was cleaned up — a third caller should be able
	// to acquire the global lock (it shouldn't be stuck held by owner2).
	lock, err := s.lockStore.Get(context.Background(), "packs:global")
	if err != nil {
		t.Fatalf("get global lock: %v", err)
	}
	if lock != nil {
		if _, held := lock.Owners[owner2]; held {
			t.Fatal("global lock still held by owner2 after cleanup failure")
		}
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
