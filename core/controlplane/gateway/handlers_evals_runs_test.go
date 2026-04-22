package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/cordum/cordum/core/evals/runner"
	"github.com/cordum/cordum/core/infra/locks"
	"github.com/cordum/cordum/core/model"
)

func bindEvalRunRoutes(t *testing.T, s *server) *http.ServeMux {
	t.Helper()
	evalRunRateLimiter.reset()
	t.Cleanup(func() {
		evalRunRateLimiter.reset()
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/evals/datasets/", s.handleEvalDatasetSubroutes)
	mux.HandleFunc("GET /api/v1/evals/runs/{run_id}", s.handleGetEvalRun)
	mux.HandleFunc("DELETE /api/v1/evals/runs/{run_id}", s.handleDeleteEvalRun)
	return mux
}

type evalRunEntrySpec struct {
	ID       string
	Topic    string
	Expected model.SafetyDecision
}

func seedEvalRunDataset(t *testing.T, s *server, name string, version int, specs []evalRunEntrySpec) model.EvalDataset {
	t.Helper()
	entries := make([]model.EvalEntry, 0, len(specs))
	for _, spec := range specs {
		input, err := json.Marshal(map[string]any{
			"tenant": specTenantOrDefault(testEvalTenant),
			"topic":  spec.Topic,
		})
		if err != nil {
			t.Fatalf("marshal input: %v", err)
		}
		entries = append(entries, model.EvalEntry{
			ID:               spec.ID,
			Input:            input,
			ExpectedDecision: spec.Expected,
			Source:           model.EvalEntrySourceManual,
		})
	}
	dataset, err := s.evalDatasetStore.CreateEvalDataset(context.Background(), model.EvalDataset{
		Name:      name,
		Version:   version,
		Tenant:    testEvalTenant,
		Entries:   entries,
		CreatedBy: "tester@" + testEvalTenant,
	})
	if err != nil {
		t.Fatalf("CreateEvalDataset() error = %v", err)
	}
	return dataset
}

func specTenantOrDefault(tenant string) string {
	if tenant == "" {
		return testEvalTenant
	}
	return tenant
}

func evalRunPost(t *testing.T, mux http.Handler, datasetID string, body map[string]any, role string) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/evals/datasets/"+datasetID+"/run", bytes.NewReader(payload)),
		evalAuthCtx(testEvalTenant, role))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

func evalRunGet(t *testing.T, mux http.Handler, runID string, role string) *httptest.ResponseRecorder {
	t.Helper()
	req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/evals/runs/"+runID, nil), evalAuthCtx(testEvalTenant, role))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

func evalRunList(t *testing.T, mux http.Handler, datasetID, rawQuery, role string) *httptest.ResponseRecorder {
	t.Helper()
	req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/evals/datasets/"+datasetID+"/runs"+rawQuery, nil), evalAuthCtx(testEvalTenant, role))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

func evalRunDelete(t *testing.T, mux http.Handler, runID, rawQuery, role string) *httptest.ResponseRecorder {
	t.Helper()
	req := withAuth(httptest.NewRequest(http.MethodDelete, "/api/v1/evals/runs/"+runID+rawQuery, nil), evalAuthCtx(testEvalTenant, role))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

func TestHandleRunEvalDatasetSyncReturns200AndStoresHistory(t *testing.T) {
	prev := evalRunAsyncSpawn
	evalRunAsyncSpawn = func(fn func()) { fn() }
	t.Cleanup(func() { evalRunAsyncSpawn = prev })

	s, _, _ := newTestGateway(t)
	s.auth = testAuthProvider{}
	seedPolicyBundle(t, s, "secops/base", `rules:
  - id: allow-all
    match:
      topics:
        - job.*
    decision: allow
`)
	dataset := seedEvalRunDataset(t, s, "sync-pack", 1, []evalRunEntrySpec{
		{ID: "entry-1", Topic: "job.deploy", Expected: model.SafetyAllow},
	})

	mux := bindEvalRunRoutes(t, s)
	rr := evalRunPost(t, mux, dataset.ID, map[string]any{"use_current_policy": true}, "admin")
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}

	var result runner.RunResult
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result.RunID == "" || result.DatasetID != dataset.ID {
		t.Fatalf("unexpected result: %#v", result)
	}
	if result.Summary.Total != 1 || result.Summary.Passed != 1 || result.Summary.Regressions != 0 {
		t.Fatalf("unexpected summary: %#v", result.Summary)
	}
	stored, err := s.evalRunStore.GetRun(context.Background(), testEvalTenant, result.RunID)
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	if stored.RunID != result.RunID {
		t.Fatalf("stored run_id=%q want %q", stored.RunID, result.RunID)
	}
}

func TestHandleRunEvalDatasetAsyncAcceptedAndPollCompletes(t *testing.T) {
	prev := evalRunAsyncSpawn
	evalRunAsyncSpawn = func(fn func()) { go fn() }
	t.Cleanup(func() { evalRunAsyncSpawn = prev })

	s, _, _ := newTestGateway(t)
	s.auth = testAuthProvider{}
	seedPolicyBundle(t, s, "secops/base", `rules:
  - id: allow-all
    match:
      topics:
        - job.*
    decision: allow
`)

	specs := make([]evalRunEntrySpec, 0, 501)
	for i := 0; i < 501; i++ {
		specs = append(specs, evalRunEntrySpec{
			ID:       "entry-" + strconv.Itoa(i),
			Topic:    "job.deploy",
			Expected: model.SafetyAllow,
		})
	}
	dataset := seedEvalRunDataset(t, s, "async-pack", 1, specs)

	mux := bindEvalRunRoutes(t, s)
	rr := evalRunPost(t, mux, dataset.ID, map[string]any{"use_current_policy": true}, "admin")
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}

	var accepted evalRunAcceptedResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &accepted); err != nil {
		t.Fatalf("decode accepted response: %v", err)
	}
	if accepted.RunID == "" {
		t.Fatalf("expected run id, got %#v", accepted)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		poll := evalRunGet(t, mux, accepted.RunID, "admin")
		if poll.Code == http.StatusOK {
			var result runner.RunResult
			if err := json.Unmarshal(poll.Body.Bytes(), &result); err != nil {
				t.Fatalf("decode poll response: %v", err)
			}
			if result.Summary.Total != 501 || result.Summary.Passed != 501 {
				t.Fatalf("unexpected async summary: %#v", result.Summary)
			}
			return
		}
		if poll.Code != http.StatusAccepted {
			t.Fatalf("unexpected poll status=%d body=%s", poll.Code, poll.Body.String())
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for async run completion: %s", poll.Body.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestHandleListEvalRunsFiltersByRegressionAndPaginates(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = testAuthProvider{}
	dataset := seedEvalRunDataset(t, s, "list-pack", 1, []evalRunEntrySpec{
		{ID: "entry-1", Topic: "job.deploy", Expected: model.SafetyAllow},
	})

	base := time.Date(2026, time.April, 21, 10, 0, 0, 0, time.UTC)
	for _, result := range []runner.RunResult{
		{
			RunID:          "run-regression-1",
			DatasetID:      dataset.ID,
			DatasetName:    dataset.Name,
			DatasetVersion: dataset.Version,
			Tenant:         testEvalTenant,
			PolicySnapshot: "snap-a",
			StartedAt:      base.Add(2 * time.Minute),
			CompletedAt:    base.Add(2*time.Minute + time.Second),
			Summary:        runner.RunSummary{Total: 1, Passed: 0, Regressions: 1},
		},
		{
			RunID:          "run-clean",
			DatasetID:      dataset.ID,
			DatasetName:    dataset.Name,
			DatasetVersion: dataset.Version,
			Tenant:         testEvalTenant,
			PolicySnapshot: "snap-b",
			StartedAt:      base.Add(1 * time.Minute),
			CompletedAt:    base.Add(1*time.Minute + time.Second),
			Summary:        runner.RunSummary{Total: 1, Passed: 1},
		},
		{
			RunID:          "run-regression-2",
			DatasetID:      dataset.ID,
			DatasetName:    dataset.Name,
			DatasetVersion: dataset.Version,
			Tenant:         testEvalTenant,
			PolicySnapshot: "snap-c",
			StartedAt:      base,
			CompletedAt:    base.Add(time.Second),
			Summary:        runner.RunSummary{Total: 1, Failed: 1, Regressions: 1},
		},
	} {
		if err := s.evalRunStore.CreateRun(context.Background(), result); err != nil {
			t.Fatalf("CreateRun(%s) error = %v", result.RunID, err)
		}
	}

	mux := bindEvalRunRoutes(t, s)
	rr := evalRunList(t, mux, dataset.ID, "?has_regression=true&limit=1", "viewer")
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var page1 evalRunListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &page1); err != nil {
		t.Fatalf("decode page 1: %v", err)
	}
	if len(page1.Items) != 1 || page1.Items[0].RunID != "run-regression-1" {
		t.Fatalf("unexpected page1: %#v", page1)
	}
	if page1.NextCursor == "" {
		t.Fatalf("expected next cursor, got %#v", page1)
	}

	rr = evalRunList(t, mux, dataset.ID, "?has_regression=true&limit=1&cursor="+page1.NextCursor, "viewer")
	if rr.Code != http.StatusOK {
		t.Fatalf("page2 status=%d body=%s", rr.Code, rr.Body.String())
	}
	var page2 evalRunListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &page2); err != nil {
		t.Fatalf("decode page 2: %v", err)
	}
	if len(page2.Items) != 1 || page2.Items[0].RunID != "run-regression-2" {
		t.Fatalf("unexpected page2: %#v", page2)
	}
}

func TestHandleRunEvalDatasetReturns404WhenDatasetMissing(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = testAuthProvider{}
	seedPolicyBundle(t, s, "secops/base", `rules:
  - id: allow-all
    match:
      topics:
        - job.*
    decision: allow
`)

	mux := bindEvalRunRoutes(t, s)
	rr := evalRunPost(t, mux, "missing-dataset", map[string]any{"use_current_policy": true}, "admin")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleRunEvalDatasetReturns400OnInvalidCandidatePolicy(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = testAuthProvider{}
	dataset := seedEvalRunDataset(t, s, "invalid-policy-pack", 1, []evalRunEntrySpec{
		{ID: "entry-1", Topic: "job.deploy", Expected: model.SafetyAllow},
	})

	mux := bindEvalRunRoutes(t, s)
	rr := evalRunPost(t, mux, dataset.ID, map[string]any{
		"candidate_content": "rules:\n  - id: broken\n    decision: definitely_not_a_real_decision\n",
	}, "admin")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleRunEvalDatasetRequiresAdmin(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = testAuthProvider{}
	seedPolicyBundle(t, s, "secops/base", `rules:
  - id: allow-all
    match:
      topics:
        - job.*
    decision: allow
`)
	dataset := seedEvalRunDataset(t, s, "auth-pack", 1, []evalRunEntrySpec{
		{ID: "entry-1", Topic: "job.deploy", Expected: model.SafetyAllow},
	})

	mux := bindEvalRunRoutes(t, s)
	rr := evalRunPost(t, mux, dataset.ID, map[string]any{"use_current_policy": true}, "viewer")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleRunEvalDatasetReturns409WhenAsyncRunAlreadyInFlight(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = testAuthProvider{}
	seedPolicyBundle(t, s, "secops/base", `rules:
  - id: allow-all
    match:
      topics:
        - job.*
    decision: allow
`)
	specs := make([]evalRunEntrySpec, 0, 501)
	for i := 0; i < 501; i++ {
		specs = append(specs, evalRunEntrySpec{
			ID:       "entry-" + strconv.Itoa(i),
			Topic:    "job.deploy",
			Expected: model.SafetyAllow,
		})
	}
	dataset := seedEvalRunDataset(t, s, "conflict-pack", 1, specs)
	if _, ok, err := s.lockStore.Acquire(context.Background(), evalRunLockResource(testEvalTenant, dataset.ID), "other-run", locks.ModeExclusive, time.Minute); err != nil || !ok {
		t.Fatalf("Acquire() ok=%v err=%v", ok, err)
	}

	mux := bindEvalRunRoutes(t, s)
	rr := evalRunPost(t, mux, dataset.ID, map[string]any{"use_current_policy": true}, "admin")
	if rr.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleDeleteEvalRunForceRoundTrip(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = testAuthProvider{}
	dataset := seedEvalRunDataset(t, s, "delete-pack", 1, []evalRunEntrySpec{
		{ID: "entry-1", Topic: "job.deploy", Expected: model.SafetyAllow},
	})
	result := runner.RunResult{
		RunID:          "run-delete-me",
		DatasetID:      dataset.ID,
		DatasetName:    dataset.Name,
		DatasetVersion: dataset.Version,
		Tenant:         testEvalTenant,
		PolicySnapshot: "snap-delete",
		StartedAt:      time.Date(2026, time.April, 21, 9, 0, 0, 0, time.UTC),
		CompletedAt:    time.Date(2026, time.April, 21, 9, 0, 1, 0, time.UTC),
		Summary:        runner.RunSummary{Total: 1, Passed: 1},
	}
	if err := s.evalRunStore.CreateRun(context.Background(), result); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}

	mux := bindEvalRunRoutes(t, s)
	rr := evalRunDelete(t, mux, result.RunID, "?force=true", "admin")
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", rr.Code, rr.Body.String())
	}

	rr = evalRunGet(t, mux, result.RunID, "admin")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("get-after-delete status=%d body=%s", rr.Code, rr.Body.String())
	}
}
