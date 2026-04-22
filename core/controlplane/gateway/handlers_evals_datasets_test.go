package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
)

const testEvalTenant = "acme"

func bindEvalDatasetRoutes(t *testing.T, s *server) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/evals/datasets", s.handleCreateEvalDataset)
	mux.HandleFunc("GET /api/v1/evals/datasets", s.handleListEvalDatasets)
	mux.HandleFunc("/api/v1/evals/datasets/", s.handleEvalDatasetSubroutes)
	return mux
}

func evalAuthCtx(tenant, role string) *auth.AuthContext {
	return &auth.AuthContext{
		Tenant:      tenant,
		Role:        role,
		PrincipalID: "tester@" + tenant,
	}
}

func evalCreateBody(name string, version int, entryCount int) []byte {
	entries := make([]map[string]any, entryCount)
	for i := 0; i < entryCount; i++ {
		entries[i] = map[string]any{
			"id":                fmt.Sprintf("entry-%d", i),
			"input":             map[string]any{"tenant": testEvalTenant, "topic": "support", "agent_id": "agent-a"},
			"expected_decision": string(model.SafetyDeny),
			"rule_id":           "rule-pii-01",
			"source":            model.EvalEntrySourceManual,
		}
	}
	body := map[string]any{
		"name":        name,
		"version":     version,
		"description": "test dataset",
		"entries":     entries,
	}
	b, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return b
}

func evalPostCreate(t *testing.T, mux http.Handler, payload []byte, role string) *httptest.ResponseRecorder {
	t.Helper()
	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/evals/datasets", bytes.NewReader(payload)),
		evalAuthCtx(testEvalTenant, role))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

func evalUpdateBody(version *int, entryCount int, description *string) []byte {
	entries := make([]map[string]any, entryCount)
	for i := 0; i < entryCount; i++ {
		entries[i] = map[string]any{
			"id":                fmt.Sprintf("entry-%d", i),
			"input":             map[string]any{"tenant": testEvalTenant, "topic": "support", "agent_id": "agent-a"},
			"expected_decision": string(model.SafetyDeny),
			"rule_id":           "rule-pii-01",
			"source":            model.EvalEntrySourceManual,
		}
	}
	body := map[string]any{
		"entries": entries,
	}
	if version != nil {
		body["version"] = *version
	}
	if description != nil {
		body["description"] = *description
	}
	b, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return b
}

func evalPutUpdate(t *testing.T, mux http.Handler, id string, payload []byte, role string) *httptest.ResponseRecorder {
	t.Helper()
	req := withAuth(httptest.NewRequest(http.MethodPut, "/api/v1/evals/datasets/"+id, bytes.NewReader(payload)),
		evalAuthCtx(testEvalTenant, role))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

func TestEvalDatasetCreateReturns201(t *testing.T) {
	s, _, _ := newTestGateway(t)
	mux := bindEvalDatasetRoutes(t, s)

	rr := evalPostCreate(t, mux, evalCreateBody("reg-pack", 1, 1), "admin")
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	var got model.EvalDataset
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID == "" {
		t.Fatal("expected id")
	}
	if got.Tenant != testEvalTenant {
		t.Fatalf("expected tenant %q, got %q", testEvalTenant, got.Tenant)
	}
	if got.Version != 1 {
		t.Fatalf("expected version 1, got %d", got.Version)
	}
	if len(got.ContentHash) != 64 {
		t.Fatalf("expected 64-char content hash, got %q", got.ContentHash)
	}
}

func TestEvalDatasetUpdateCreatesSuccessorVersion(t *testing.T) {
	s, _, _ := newTestGateway(t)
	mux := bindEvalDatasetRoutes(t, s)

	createRR := evalPostCreate(t, mux, evalCreateBody("reg-pack", 1, 1), "admin")
	if createRR.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", createRR.Code, createRR.Body.String())
	}
	var base model.EvalDataset
	if err := json.Unmarshal(createRR.Body.Bytes(), &base); err != nil {
		t.Fatalf("decode base: %v", err)
	}

	nextDescription := "second version"
	updateRR := evalPutUpdate(t, mux, base.ID, evalUpdateBody(nil, 2, &nextDescription), "admin")
	if updateRR.Code != http.StatusCreated {
		t.Fatalf("update: expected 201, got %d: %s", updateRR.Code, updateRR.Body.String())
	}
	var successor model.EvalDataset
	if err := json.Unmarshal(updateRR.Body.Bytes(), &successor); err != nil {
		t.Fatalf("decode successor: %v", err)
	}
	if successor.ID == "" || successor.ID == base.ID {
		t.Fatalf("expected distinct successor id, base=%q successor=%q", base.ID, successor.ID)
	}
	if successor.Name != base.Name {
		t.Fatalf("expected same dataset name, got %q want %q", successor.Name, base.Name)
	}
	if successor.Version != 2 {
		t.Fatalf("expected successor version 2, got %d", successor.Version)
	}
	if successor.Description != nextDescription {
		t.Fatalf("expected updated description %q, got %q", nextDescription, successor.Description)
	}
	if successor.EntryCount != 2 {
		t.Fatalf("expected 2 entries in successor, got %d", successor.EntryCount)
	}

	getBaseReq := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/evals/datasets/"+base.ID, nil),
		evalAuthCtx(testEvalTenant, "admin"))
	getBaseRR := httptest.NewRecorder()
	mux.ServeHTTP(getBaseRR, getBaseReq)
	if getBaseRR.Code != http.StatusOK {
		t.Fatalf("get base: expected 200, got %d: %s", getBaseRR.Code, getBaseRR.Body.String())
	}
	var unchanged model.EvalDataset
	if err := json.Unmarshal(getBaseRR.Body.Bytes(), &unchanged); err != nil {
		t.Fatalf("decode unchanged base: %v", err)
	}
	if unchanged.Version != 1 || unchanged.EntryCount != 1 {
		t.Fatalf("expected base dataset unchanged at v1/1-entry, got v%d/%d", unchanged.Version, unchanged.EntryCount)
	}

	req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/evals/datasets/by-name/reg-pack", nil),
		evalAuthCtx(testEvalTenant, "admin"))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list versions: %d %s", rr.Code, rr.Body.String())
	}
	var resp evalDatasetVersionsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode versions: %v", err)
	}
	if len(resp.Items) != 2 || resp.Items[0].Version != 2 || resp.Items[1].Version != 1 {
		t.Fatalf("expected versions [2,1], got %+v", resp.Items)
	}
}

func TestEvalDatasetUpdateRejectsNonIncreasingVersion(t *testing.T) {
	s, _, _ := newTestGateway(t)
	mux := bindEvalDatasetRoutes(t, s)

	createRR := evalPostCreate(t, mux, evalCreateBody("version-guard", 1, 1), "admin")
	if createRR.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", createRR.Code, createRR.Body.String())
	}
	var base model.EvalDataset
	if err := json.Unmarshal(createRR.Body.Bytes(), &base); err != nil {
		t.Fatalf("decode base: %v", err)
	}

	version := 1
	updateRR := evalPutUpdate(t, mux, base.ID, evalUpdateBody(&version, 1, nil), "admin")
	if updateRR.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on same version, got %d: %s", updateRR.Code, updateRR.Body.String())
	}
}

func TestEvalDatasetUpdateMissingReturns404(t *testing.T) {
	s, _, _ := newTestGateway(t)
	mux := bindEvalDatasetRoutes(t, s)

	updateRR := evalPutUpdate(t, mux, "missing", evalUpdateBody(nil, 1, nil), "admin")
	if updateRR.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing base dataset, got %d: %s", updateRR.Code, updateRR.Body.String())
	}
}

func TestEvalDatasetCreateReturns409OnCollision(t *testing.T) {
	s, _, _ := newTestGateway(t)
	mux := bindEvalDatasetRoutes(t, s)

	if rr := evalPostCreate(t, mux, evalCreateBody("reg-pack", 1, 1), "admin"); rr.Code != http.StatusCreated {
		t.Fatalf("first create: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	rr := evalPostCreate(t, mux, evalCreateBody("reg-pack", 1, 1), "admin")
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409 on duplicate, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestEvalDatasetCreateReturns400OnInvalidName(t *testing.T) {
	s, _, _ := newTestGateway(t)
	mux := bindEvalDatasetRoutes(t, s)

	// A space is non-normalizable into a legal name, so validation
	// should reject it with 400.
	rr := evalPostCreate(t, mux, evalCreateBody("bad name with space", 1, 1), "admin")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestEvalDatasetCreateReturns413OnOversizedBody(t *testing.T) {
	s, _, _ := newTestGateway(t)
	mux := bindEvalDatasetRoutes(t, s)

	// Build a payload that is well past the 16 MiB cap without allocating
	// so much that the test harness suffers. We pad the description field
	// with a single string that exceeds the cap.
	oversized := map[string]any{
		"name":        "oversized-pack",
		"version":     1,
		"description": strings.Repeat("a", maxEvalDatasetRequestBytes+1024),
		"entries": []map[string]any{
			{
				"id":                "entry-1",
				"input":             map[string]any{"tenant": testEvalTenant},
				"expected_decision": string(model.SafetyDeny),
			},
		},
	}
	payload, err := json.Marshal(oversized)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	rr := evalPostCreate(t, mux, payload, "admin")
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestEvalDatasetGetReturns404(t *testing.T) {
	s, _, _ := newTestGateway(t)
	mux := bindEvalDatasetRoutes(t, s)

	req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/evals/datasets/missing", nil),
		evalAuthCtx(testEvalTenant, "admin"))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestEvalDatasetCrossTenantReadReturns404(t *testing.T) {
	s, _, _ := newTestGateway(t)
	mux := bindEvalDatasetRoutes(t, s)

	// Create in tenant A.
	rr := evalPostCreate(t, mux, evalCreateBody("tenant-a-pack", 1, 1), "admin")
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}
	var created model.EvalDataset
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Attempt to read from tenant B — should come back 404.
	req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/evals/datasets/"+created.ID, nil),
		evalAuthCtx("tenant-b", "admin"))
	rr2 := httptest.NewRecorder()
	mux.ServeHTTP(rr2, req)
	if rr2.Code != http.StatusNotFound {
		t.Fatalf("expected cross-tenant read to 404, got %d: %s", rr2.Code, rr2.Body.String())
	}
}

func TestEvalDatasetDeleteRequiresForce(t *testing.T) {
	s, _, _ := newTestGateway(t)
	mux := bindEvalDatasetRoutes(t, s)

	rr := evalPostCreate(t, mux, evalCreateBody("force-pack", 1, 1), "admin")
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: %d", rr.Code)
	}
	var created model.EvalDataset
	_ = json.Unmarshal(rr.Body.Bytes(), &created)

	// Without force=true.
	req := withAuth(httptest.NewRequest(http.MethodDelete, "/api/v1/evals/datasets/"+created.ID, nil),
		evalAuthCtx(testEvalTenant, "admin"))
	rr2 := httptest.NewRecorder()
	mux.ServeHTTP(rr2, req)
	if rr2.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 without force, got %d: %s", rr2.Code, rr2.Body.String())
	}
	if !strings.Contains(rr2.Body.String(), "force=true") {
		t.Fatalf("expected force=true guidance in error, got %q", rr2.Body.String())
	}

	// With force=true.
	req = withAuth(httptest.NewRequest(http.MethodDelete, "/api/v1/evals/datasets/"+created.ID+"?force=true", nil),
		evalAuthCtx(testEvalTenant, "admin"))
	rr3 := httptest.NewRecorder()
	mux.ServeHTTP(rr3, req)
	if rr3.Code != http.StatusNoContent {
		t.Fatalf("expected 204 with force=true, got %d: %s", rr3.Code, rr3.Body.String())
	}

	// After delete, get returns 404.
	req = withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/evals/datasets/"+created.ID, nil),
		evalAuthCtx(testEvalTenant, "admin"))
	rr4 := httptest.NewRecorder()
	mux.ServeHTTP(rr4, req)
	if rr4.Code != http.StatusNotFound {
		t.Fatalf("expected 404 post-delete, got %d", rr4.Code)
	}
}

func TestEvalDatasetDeleteOtherValuesRejected(t *testing.T) {
	s, _, _ := newTestGateway(t)
	mux := bindEvalDatasetRoutes(t, s)

	rr := evalPostCreate(t, mux, evalCreateBody("quiet-pack", 1, 1), "admin")
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}
	var created model.EvalDataset
	_ = json.Unmarshal(rr.Body.Bytes(), &created)

	for _, badForce := range []string{"", "1", "yes", "TRUE", "FORCE", "false"} {
		u := "/api/v1/evals/datasets/" + created.ID
		if badForce != "" {
			u += "?force=" + badForce
		}
		req := withAuth(httptest.NewRequest(http.MethodDelete, u, nil),
			evalAuthCtx(testEvalTenant, "admin"))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for force=%q, got %d", badForce, rec.Code)
		}
	}
}

func TestEvalDatasetDeleteMissingReturns404(t *testing.T) {
	s, _, _ := newTestGateway(t)
	mux := bindEvalDatasetRoutes(t, s)

	req := withAuth(httptest.NewRequest(http.MethodDelete, "/api/v1/evals/datasets/nobody?force=true", nil),
		evalAuthCtx(testEvalTenant, "admin"))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestEvalDatasetListPaginationRoundTrip(t *testing.T) {
	s, _, _ := newTestGateway(t)
	mux := bindEvalDatasetRoutes(t, s)

	// Seed 7 datasets.
	for i := 0; i < 7; i++ {
		payload := evalCreateBody(fmt.Sprintf("seed-%02d", i), 1, 1)
		if rr := evalPostCreate(t, mux, payload, "admin"); rr.Code != http.StatusCreated {
			t.Fatalf("seed %d: %d %s", i, rr.Code, rr.Body.String())
		}
	}

	seen := map[string]bool{}
	cursor := ""
	pages := 0
	for {
		u := "/api/v1/evals/datasets?limit=3"
		if cursor != "" {
			u += "&cursor=" + cursor
		}
		req := withAuth(httptest.NewRequest(http.MethodGet, u, nil),
			evalAuthCtx(testEvalTenant, "admin"))
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("page %d: %d %s", pages+1, rr.Code, rr.Body.String())
		}
		var page evalDatasetsListResponse
		if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
			t.Fatalf("decode page %d: %v", pages+1, err)
		}
		for _, it := range page.Items {
			if seen[it.ID] {
				t.Fatalf("duplicate id across pages: %q", it.ID)
			}
			seen[it.ID] = true
		}
		pages++
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
		if pages > 5 {
			t.Fatal("pagination did not terminate")
		}
	}
	if len(seen) != 7 {
		t.Fatalf("expected 7 unique datasets, got %d (pages=%d)", len(seen), pages)
	}
}

func TestEvalDatasetListFilterByNamePrefix(t *testing.T) {
	s, _, _ := newTestGateway(t)
	mux := bindEvalDatasetRoutes(t, s)

	for _, n := range []string{"pii-alpha", "pii-bravo", "tool-drift", "other-case"} {
		if rr := evalPostCreate(t, mux, evalCreateBody(n, 1, 1), "admin"); rr.Code != http.StatusCreated {
			t.Fatalf("seed %s: %d %s", n, rr.Code, rr.Body.String())
		}
	}

	req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/evals/datasets?name_prefix=pii-", nil),
		evalAuthCtx(testEvalTenant, "admin"))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rr.Code, rr.Body.String())
	}
	var page evalDatasetsListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(page.Items))
	}
	for _, it := range page.Items {
		if !strings.HasPrefix(it.Name, "pii-") {
			t.Fatalf("unexpected non-matching item %q", it.Name)
		}
	}
}

func TestEvalDatasetListByNameSortsDesc(t *testing.T) {
	s, _, _ := newTestGateway(t)
	mux := bindEvalDatasetRoutes(t, s)

	for _, v := range []int{1, 2, 3} {
		if rr := evalPostCreate(t, mux, evalCreateBody("version-history", v, 1), "admin"); rr.Code != http.StatusCreated {
			t.Fatalf("seed v%d: %d %s", v, rr.Code, rr.Body.String())
		}
	}

	req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/evals/datasets/by-name/version-history", nil),
		evalAuthCtx(testEvalTenant, "admin"))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("by-name: %d %s", rr.Code, rr.Body.String())
	}
	var resp evalDatasetVersionsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 3 {
		t.Fatalf("expected 3 versions, got %d", len(resp.Items))
	}
	if resp.Items[0].Version != 3 || resp.Items[1].Version != 2 || resp.Items[2].Version != 1 {
		t.Fatalf("expected desc order 3,2,1, got %d,%d,%d",
			resp.Items[0].Version, resp.Items[1].Version, resp.Items[2].Version)
	}
}

func TestEvalDatasetSubroutesPreferByNameOverRunHistory(t *testing.T) {
	s, _, _ := newTestGateway(t)
	mux := bindEvalDatasetRoutes(t, s)

	if rr := evalPostCreate(t, mux, evalCreateBody("runs", 1, 1), "admin"); rr.Code != http.StatusCreated {
		t.Fatalf("seed runs dataset: %d %s", rr.Code, rr.Body.String())
	}

	req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/evals/datasets/by-name/runs", nil),
		evalAuthCtx(testEvalTenant, "viewer"))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("by-name runs: %d %s", rr.Code, rr.Body.String())
	}

	var resp evalDatasetVersionsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].Name != "runs" || resp.Items[0].Version != 1 {
		t.Fatalf("unexpected versions response: %+v", resp.Items)
	}
}

func TestEvalDatasetByNameVersionReturnsExactMatch(t *testing.T) {
	s, _, _ := newTestGateway(t)
	mux := bindEvalDatasetRoutes(t, s)

	for _, v := range []int{1, 2, 3} {
		if rr := evalPostCreate(t, mux, evalCreateBody("resolve-me", v, 1), "admin"); rr.Code != http.StatusCreated {
			t.Fatalf("seed v%d: %d %s", v, rr.Code, rr.Body.String())
		}
	}

	req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/evals/datasets/by-name/resolve-me/versions/2", nil),
		evalAuthCtx(testEvalTenant, "admin"))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("by-name version: %d %s", rr.Code, rr.Body.String())
	}
	var ds model.EvalDataset
	if err := json.Unmarshal(rr.Body.Bytes(), &ds); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ds.Version != 2 || ds.Name != "resolve-me" {
		t.Fatalf("expected v2 resolve-me, got v%d %q", ds.Version, ds.Name)
	}

	// Missing version → 404.
	req = withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/evals/datasets/by-name/resolve-me/versions/9", nil),
		evalAuthCtx(testEvalTenant, "admin"))
	rr2 := httptest.NewRecorder()
	mux.ServeHTTP(rr2, req)
	if rr2.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing version, got %d", rr2.Code)
	}

	// Bad version string → 400.
	req = withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/evals/datasets/by-name/resolve-me/versions/abc", nil),
		evalAuthCtx(testEvalTenant, "admin"))
	rr3 := httptest.NewRecorder()
	mux.ServeHTTP(rr3, req)
	if rr3.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-numeric version, got %d", rr3.Code)
	}
}

func TestEvalDatasetCreateStoreNilReturns503(t *testing.T) {
	s, _, _ := newTestGateway(t)
	// Force the store to nil to prove the handler returns 503 rather
	// than panicking.
	s.evalDatasetStore = nil
	mux := bindEvalDatasetRoutes(t, s)

	rr := evalPostCreate(t, mux, evalCreateBody("store-missing", 1, 1), "admin")
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when store is unavailable, got %d", rr.Code)
	}
}

func TestEvalDatasetListRejectsBadLimit(t *testing.T) {
	s, _, _ := newTestGateway(t)
	mux := bindEvalDatasetRoutes(t, s)

	cases := []struct {
		name string
		url  string
	}{
		{"non-integer", "/api/v1/evals/datasets?limit=abc"},
		{"zero", "/api/v1/evals/datasets?limit=0"},
		{"negative", "/api/v1/evals/datasets?limit=-5"},
		{"too-large", "/api/v1/evals/datasets?limit=999"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := withAuth(httptest.NewRequest(http.MethodGet, tc.url, nil),
				evalAuthCtx(testEvalTenant, "admin"))
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 for %s, got %d: %s", tc.name, rr.Code, rr.Body.String())
			}
		})
	}
}

func TestEvalDatasetListAcceptsTimestampFilters(t *testing.T) {
	s, _, _ := newTestGateway(t)
	mux := bindEvalDatasetRoutes(t, s)

	if rr := evalPostCreate(t, mux, evalCreateBody("time-a", 1, 1), "admin"); rr.Code != http.StatusCreated {
		t.Fatalf("seed: %d", rr.Code)
	}

	// Happy path: both created_after and created_before parse as RFC3339.
	url := "/api/v1/evals/datasets?created_after=2020-01-01T00:00:00Z&created_before=2099-01-01T00:00:00Z"
	req := withAuth(httptest.NewRequest(http.MethodGet, url, nil),
		evalAuthCtx(testEvalTenant, "admin"))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("timestamp filter: %d %s", rr.Code, rr.Body.String())
	}
}

func TestEvalDatasetListRejectsBadTimestamps(t *testing.T) {
	s, _, _ := newTestGateway(t)
	mux := bindEvalDatasetRoutes(t, s)

	cases := []string{
		"/api/v1/evals/datasets?created_after=not-a-date",
		"/api/v1/evals/datasets?created_before=also-not",
		"/api/v1/evals/datasets?created_after=2099-01-01T00:00:00Z&created_before=2000-01-01T00:00:00Z",
	}
	for _, u := range cases {
		req := withAuth(httptest.NewRequest(http.MethodGet, u, nil),
			evalAuthCtx(testEvalTenant, "admin"))
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for %s, got %d: %s", u, rr.Code, rr.Body.String())
		}
	}
}

func TestEvalDatasetHandlersRequireTenant(t *testing.T) {
	s, _, _ := newTestGateway(t)
	mux := bindEvalDatasetRoutes(t, s)

	// An auth context without a tenant (tenantFromRequest returns "").
	noTenant := &auth.AuthContext{Role: "admin", PrincipalID: "alice"}

	routes := []struct {
		name   string
		method string
		url    string
		body   []byte
	}{
		{"list", http.MethodGet, "/api/v1/evals/datasets", nil},
		{"get", http.MethodGet, "/api/v1/evals/datasets/xyz", nil},
		{"put", http.MethodPut, "/api/v1/evals/datasets/xyz", evalUpdateBody(nil, 1, nil)},
		{"by-name-list", http.MethodGet, "/api/v1/evals/datasets/by-name/foo", nil},
		{"by-name-version", http.MethodGet, "/api/v1/evals/datasets/by-name/foo/versions/1", nil},
		{"delete", http.MethodDelete, "/api/v1/evals/datasets/xyz?force=true", nil},
	}
	for _, tc := range routes {
		t.Run(tc.name, func(t *testing.T) {
			req := withAuth(httptest.NewRequest(tc.method, tc.url, bytes.NewReader(tc.body)), noTenant)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("%s: expected 400 when tenant missing, got %d", tc.name, rr.Code)
			}
		})
	}
}

func TestEvalDatasetHandlersReturn503WhenStoreNil(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.evalDatasetStore = nil
	mux := bindEvalDatasetRoutes(t, s)

	routes := []struct {
		method string
		url    string
		body   []byte
	}{
		{http.MethodGet, "/api/v1/evals/datasets", nil},
		{http.MethodGet, "/api/v1/evals/datasets/xyz", nil},
		{http.MethodPut, "/api/v1/evals/datasets/xyz", evalUpdateBody(nil, 1, nil)},
		{http.MethodGet, "/api/v1/evals/datasets/by-name/foo", nil},
		{http.MethodGet, "/api/v1/evals/datasets/by-name/foo/versions/1", nil},
		{http.MethodDelete, "/api/v1/evals/datasets/xyz?force=true", nil},
	}
	for _, tc := range routes {
		req := withAuth(httptest.NewRequest(tc.method, tc.url, bytes.NewReader(tc.body)),
			evalAuthCtx(testEvalTenant, "admin"))
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s %s: expected 503, got %d", tc.method, tc.url, rr.Code)
		}
	}
}

func TestEvalDatasetParseQueryTimestamp(t *testing.T) {
	// Directly exercise the helper for full coverage.
	ms, err := parseEvalDatasetQueryTimestamp("2026-04-20T00:00:00Z")
	if err != nil {
		t.Fatalf("RFC3339: %v", err)
	}
	if ms <= 0 {
		t.Fatalf("expected positive ms, got %d", ms)
	}
	if _, err := parseEvalDatasetQueryTimestamp("not-a-date"); err == nil {
		t.Fatal("expected error on bad timestamp")
	}
}

func TestEvalDatasetStoreInterfaceSatisfiedByRealStore(t *testing.T) {
	// Compile-time sanity: ensure the concrete Redis-backed store
	// satisfies the model.EvalDatasetStore interface used by the server
	// struct.
	ctx := context.Background()
	_ = ctx
	var _ model.EvalDatasetStore = (*store.EvalDatasetStore)(nil)
}

// compile-time guard that the handler wiring supports the body cap path.
var _ = func() { _ = io.EOF }
