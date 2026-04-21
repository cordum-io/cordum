package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/licensing"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

type extractionDatasetStore struct {
	versions     []model.EvalDataset
	created      []model.EvalDataset
	createErrors []error
}

func (s *extractionDatasetStore) CreateEvalDataset(_ context.Context, dataset model.EvalDataset) (model.EvalDataset, error) {
	s.created = append(s.created, dataset)
	if len(s.createErrors) > 0 {
		err := s.createErrors[0]
		s.createErrors = s.createErrors[1:]
		if err != nil {
			return model.EvalDataset{}, err
		}
	}
	created := dataset
	created.ID = "dataset-1"
	created.EntryCount = len(created.Entries)
	return created, nil
}

func (s *extractionDatasetStore) GetEvalDataset(context.Context, string, string) (model.EvalDataset, error) {
	return model.EvalDataset{}, nil
}

func (s *extractionDatasetStore) ListEvalDatasets(context.Context, string, model.EvalDatasetFilter, string, int) (model.EvalDatasetPage, error) {
	return model.EvalDatasetPage{}, nil
}

func (s *extractionDatasetStore) DeleteEvalDataset(context.Context, string, string) error { return nil }

func (s *extractionDatasetStore) GetEvalDatasetByNameVersion(context.Context, string, string, int) (model.EvalDataset, error) {
	return model.EvalDataset{}, nil
}

func (s *extractionDatasetStore) ListEvalDatasetVersions(context.Context, string, string) ([]model.EvalDataset, error) {
	return append([]model.EvalDataset(nil), s.versions...), nil
}

func bindEvalExtractionRoutes(t *testing.T, s *server) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/evals/datasets/from-incidents", s.handleCreateDatasetFromIncidents)
	return mux
}

func extractionAuthCtx(tenant, role string) *auth.AuthContext {
	return &auth.AuthContext{
		Tenant:      tenant,
		Role:        role,
		PrincipalID: "extractor@" + tenant,
	}
}

func extractionBody(overrides map[string]any) []byte {
	body := map[string]any{
		"name":        "incident-pack",
		"since":       "2026-04-19T00:00:00Z",
		"until":       "2026-04-20T00:00:00Z",
		"topic":       "support.*",
		"description": "regression incidents",
	}
	for key, value := range overrides {
		body[key] = value
	}
	raw, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return raw
}

func withExtractionGlobals(t *testing.T, timeout time.Duration, now time.Time) {
	t.Helper()
	prevTimeout := incidentExtractionTimeout
	prevNow := incidentExtractionRateLimiter.now
	incidentExtractionTimeout = timeout
	incidentExtractionRateLimiter.now = func() time.Time { return now }
	incidentExtractionRateLimiter.reset()
	t.Cleanup(func() {
		incidentExtractionTimeout = prevTimeout
		incidentExtractionRateLimiter.now = prevNow
		incidentExtractionRateLimiter.reset()
	})
}

func TestHandleCreateDatasetFromIncidentsReturns201(t *testing.T) {
	now := time.Date(2026, time.April, 20, 12, 0, 0, 0, time.UTC)
	withExtractionGlobals(t, 60*time.Second, now)

	s, _, _ := newTestGateway(t)
	s.auth = governanceAuth{}
	s.decisionLogStore = &stubDecisionLogStore{
		queryFn: func(query model.DecisionQuery) (model.DecisionPage, error) {
			switch query.Verdict {
			case model.SafetyDeny:
				return model.DecisionPage{Items: []model.DecisionLogRecord{
					{JobID: "job-1", Tenant: "acme", Topic: "support.email", Verdict: model.SafetyDeny, RuleID: "rule-pii", PolicyVersion: "v2", Timestamp: now.Add(-time.Hour).UnixMilli()},
				}}, nil
			case model.SafetyRequireApproval:
				return model.DecisionPage{}, nil
			default:
				return model.DecisionPage{}, nil
			}
		},
	}
	dsStore := &extractionDatasetStore{}
	s.evalDatasetStore = dsStore

	if err := s.jobStore.SetJobRequest(context.Background(), &pb.JobRequest{
		JobId:    "job-1",
		Topic:    "support.email",
		TenantId: "acme",
		Meta: &pb.JobMetadata{
			Capability: "read",
			RiskTags:   []string{"pii"},
		},
	}); err != nil {
		t.Fatalf("SetJobRequest() error = %v", err)
	}

	mux := bindEvalExtractionRoutes(t, s)
	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/evals/datasets/from-incidents", bytes.NewReader(extractionBody(nil))), extractionAuthCtx("acme", "admin"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}

	var resp createDatasetFromIncidentsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.DatasetID == "" || resp.EntryCount != 1 {
		t.Fatalf("unexpected response: %#v", resp)
	}
	if len(dsStore.created) != 1 {
		t.Fatalf("CreateEvalDataset calls = %d want 1", len(dsStore.created))
	}
}

func TestHandleCreateDatasetFromIncidentsDryRunDoesNotWrite(t *testing.T) {
	now := time.Date(2026, time.April, 20, 12, 0, 0, 0, time.UTC)
	withExtractionGlobals(t, 60*time.Second, now)

	s, _, _ := newTestGateway(t)
	s.auth = governanceAuth{}
	s.decisionLogStore = &stubDecisionLogStore{
		queryFn: func(query model.DecisionQuery) (model.DecisionPage, error) {
			return model.DecisionPage{Items: []model.DecisionLogRecord{
				{JobID: "job-1", Tenant: "acme", Topic: "support", Verdict: model.SafetyDeny, Timestamp: now.UnixMilli()},
			}}, nil
		},
	}
	dsStore := &extractionDatasetStore{}
	s.evalDatasetStore = dsStore
	if err := s.jobStore.SetJobRequest(context.Background(), &pb.JobRequest{JobId: "job-1", Topic: "support", TenantId: "acme"}); err != nil {
		t.Fatalf("SetJobRequest() error = %v", err)
	}

	mux := bindEvalExtractionRoutes(t, s)
	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/evals/datasets/from-incidents?dry_run=true", bytes.NewReader(extractionBody(nil))), extractionAuthCtx("acme", "admin"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if len(dsStore.created) != 0 {
		t.Fatalf("dry run should not write, got %d CreateEvalDataset calls", len(dsStore.created))
	}
}

func TestHandleCreateDatasetFromIncidentsReturns404OnZeroMatches(t *testing.T) {
	now := time.Date(2026, time.April, 20, 12, 0, 0, 0, time.UTC)
	withExtractionGlobals(t, 60*time.Second, now)

	s, _, _ := newTestGateway(t)
	s.auth = governanceAuth{}
	s.decisionLogStore = &stubDecisionLogStore{}
	s.evalDatasetStore = &extractionDatasetStore{}

	mux := bindEvalExtractionRoutes(t, s)
	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/evals/datasets/from-incidents", bytes.NewReader(extractionBody(nil))), extractionAuthCtx("acme", "admin"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleCreateDatasetFromIncidentsRejectsInvalidVerdict(t *testing.T) {
	now := time.Date(2026, time.April, 20, 12, 0, 0, 0, time.UTC)
	withExtractionGlobals(t, 60*time.Second, now)

	s, _, _ := newTestGateway(t)
	s.auth = governanceAuth{}
	s.decisionLogStore = &stubDecisionLogStore{}
	s.evalDatasetStore = &extractionDatasetStore{}

	mux := bindEvalExtractionRoutes(t, s)
	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/evals/datasets/from-incidents", bytes.NewReader(extractionBody(map[string]any{"verdicts": []string{"shadowban"}}))), extractionAuthCtx("acme", "admin"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleCreateDatasetFromIncidentsRejectsInvalidSinceTimestamp(t *testing.T) {
	now := time.Date(2026, time.April, 20, 12, 0, 0, 0, time.UTC)
	withExtractionGlobals(t, 60*time.Second, now)

	s, _, _ := newTestGateway(t)
	s.auth = governanceAuth{}
	s.decisionLogStore = &stubDecisionLogStore{}
	s.evalDatasetStore = &extractionDatasetStore{}

	mux := bindEvalExtractionRoutes(t, s)
	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/evals/datasets/from-incidents", bytes.NewReader(extractionBody(map[string]any{"since": "yesterday"}))), extractionAuthCtx("acme", "admin"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleCreateDatasetFromIncidentsRejectsInvalidDryRunQuery(t *testing.T) {
	now := time.Date(2026, time.April, 20, 12, 0, 0, 0, time.UTC)
	withExtractionGlobals(t, 60*time.Second, now)

	s, _, _ := newTestGateway(t)
	s.auth = governanceAuth{}
	s.decisionLogStore = &stubDecisionLogStore{}
	s.evalDatasetStore = &extractionDatasetStore{}

	mux := bindEvalExtractionRoutes(t, s)
	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/evals/datasets/from-incidents?dry_run=maybe", bytes.NewReader(extractionBody(nil))), extractionAuthCtx("acme", "admin"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleCreateDatasetFromIncidentsTenantFromMiddlewareWins(t *testing.T) {
	now := time.Date(2026, time.April, 20, 12, 0, 0, 0, time.UTC)
	withExtractionGlobals(t, 60*time.Second, now)

	s, _, _ := newTestGateway(t)
	s.auth = governanceAuth{}
	store := &stubDecisionLogStore{
		queryFn: func(query model.DecisionQuery) (model.DecisionPage, error) {
			return model.DecisionPage{}, nil
		},
	}
	s.decisionLogStore = store
	s.evalDatasetStore = &extractionDatasetStore{}

	mux := bindEvalExtractionRoutes(t, s)
	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/evals/datasets/from-incidents", bytes.NewReader(extractionBody(map[string]any{"tenant": "tenant-b"}))), extractionAuthCtx("tenant-a", "admin"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)
	if store.lastQuery.Tenant != "tenant-a" {
		t.Fatalf("query tenant = %q want tenant-a", store.lastQuery.Tenant)
	}
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleCreateDatasetFromIncidentsRequiresAuth(t *testing.T) {
	now := time.Date(2026, time.April, 20, 12, 0, 0, 0, time.UTC)
	withExtractionGlobals(t, 60*time.Second, now)

	s, _, _ := newTestGateway(t)
	s.auth = governanceAuth{}
	s.decisionLogStore = &stubDecisionLogStore{}
	s.evalDatasetStore = &extractionDatasetStore{}

	handler := apiKeyMiddleware(s.auth, bindEvalExtractionRoutes(t, s))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/evals/datasets/from-incidents", bytes.NewReader(extractionBody(nil)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleCreateDatasetFromIncidentsRBACDenied(t *testing.T) {
	now := time.Date(2026, time.April, 20, 12, 0, 0, 0, time.UTC)
	withExtractionGlobals(t, 60*time.Second, now)

	s, _, _ := newTestGateway(t)
	s.auth = governanceAuth{}
	s.decisionLogStore = &stubDecisionLogStore{}
	s.evalDatasetStore = &extractionDatasetStore{}
	setTestEntitlements(t, s, licensing.PlanEnterprise, func(ent *licensing.Entitlements) {
		ent.RBAC = true
	})
	if err := s.rbacStore.PutRole(context.Background(), &auth.RoleDefinition{
		Name:        "governance-only",
		Permissions: []string{auth.PermGovernanceRead},
	}); err != nil {
		t.Fatalf("PutRole() error = %v", err)
	}

	mux := bindEvalExtractionRoutes(t, s)
	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/evals/datasets/from-incidents", bytes.NewReader(extractionBody(nil))), extractionAuthCtx("acme", "governance-only"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleCreateDatasetFromIncidentsReturns503WhenUnavailable(t *testing.T) {
	now := time.Date(2026, time.April, 20, 12, 0, 0, 0, time.UTC)
	withExtractionGlobals(t, 60*time.Second, now)

	s, _, _ := newTestGateway(t)
	s.auth = governanceAuth{}
	s.decisionLogStore = &stubDecisionLogStore{}
	s.evalDatasetStore = nil

	mux := bindEvalExtractionRoutes(t, s)
	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/evals/datasets/from-incidents", bytes.NewReader(extractionBody(nil))), extractionAuthCtx("acme", "admin"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleCreateDatasetFromIncidentsReturns504OnDeadlineExceeded(t *testing.T) {
	now := time.Date(2026, time.April, 20, 12, 0, 0, 0, time.UTC)
	withExtractionGlobals(t, 100*time.Millisecond, now)

	s, _, _ := newTestGateway(t)
	s.auth = governanceAuth{}
	callCount := 0
	s.decisionLogStore = &stubDecisionLogStore{
		queryFn: func(query model.DecisionQuery) (model.DecisionPage, error) {
			callCount++
			if callCount == 1 {
				return model.DecisionPage{
					Items: []model.DecisionLogRecord{
						{JobID: "job-missing", Tenant: "acme", Topic: "support.email", Verdict: model.SafetyDeny, Timestamp: now.UnixMilli()},
					},
					NextCursor: "more",
				}, nil
			}
			<-time.After(150 * time.Millisecond)
			return model.DecisionPage{}, queryContextErr()
		},
	}
	s.evalDatasetStore = &extractionDatasetStore{}

	mux := bindEvalExtractionRoutes(t, s)
	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/evals/datasets/from-incidents", bytes.NewReader(extractionBody(map[string]any{"verdicts": []string{"deny"}}))), extractionAuthCtx("acme", "admin"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusGatewayTimeout {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}

	var resp createDatasetFromIncidentsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Warnings) == 0 {
		t.Fatalf("expected partial warnings on timeout, got %#v", resp)
	}
}

func TestHandleCreateDatasetFromIncidentsReturns409OnVersionCollision(t *testing.T) {
	now := time.Date(2026, time.April, 20, 12, 0, 0, 0, time.UTC)
	withExtractionGlobals(t, 60*time.Second, now)

	s, _, _ := newTestGateway(t)
	s.auth = governanceAuth{}
	s.decisionLogStore = &stubDecisionLogStore{
		queryFn: func(query model.DecisionQuery) (model.DecisionPage, error) {
			if query.Verdict != model.SafetyDeny {
				return model.DecisionPage{}, nil
			}
			return model.DecisionPage{Items: []model.DecisionLogRecord{
				{JobID: "job-1", Tenant: "acme", Topic: "support.email", Verdict: model.SafetyDeny, Timestamp: now.UnixMilli()},
			}}, nil
		},
	}
	dsStore := &extractionDatasetStore{createErrors: []error{store.ErrEvalDatasetVersionExists, store.ErrEvalDatasetVersionExists}}
	s.evalDatasetStore = dsStore
	if err := s.jobStore.SetJobRequest(context.Background(), &pb.JobRequest{JobId: "job-1", Topic: "support.email", TenantId: "acme"}); err != nil {
		t.Fatalf("SetJobRequest() error = %v", err)
	}

	mux := bindEvalExtractionRoutes(t, s)
	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/evals/datasets/from-incidents", bytes.NewReader(extractionBody(map[string]any{"verdicts": []string{"deny"}}))), extractionAuthCtx("acme", "admin"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func queryContextErr() error {
	return context.DeadlineExceeded
}

func TestHandleCreateDatasetFromIncidentsRateLimitedPerTenant(t *testing.T) {
	now := time.Date(2026, time.April, 20, 12, 0, 0, 0, time.UTC)
	withExtractionGlobals(t, 60*time.Second, now)

	s, _, _ := newTestGateway(t)
	s.auth = governanceAuth{}
	s.decisionLogStore = &stubDecisionLogStore{}
	s.evalDatasetStore = &extractionDatasetStore{}
	mux := bindEvalExtractionRoutes(t, s)

	for i := 0; i < 6; i++ {
		req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/evals/datasets/from-incidents", bytes.NewReader(extractionBody(nil))), extractionAuthCtx("acme", "admin"))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusNotFound {
			t.Fatalf("attempt %d status=%d body=%s", i+1, rr.Code, rr.Body.String())
		}
	}

	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/evals/datasets/from-incidents", bytes.NewReader(extractionBody(nil))), extractionAuthCtx("acme", "admin"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}
