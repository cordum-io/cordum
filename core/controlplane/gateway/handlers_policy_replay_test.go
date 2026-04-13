package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// policyReplayAuth implements AuthProvider for replay tests, requiring admin role.
type policyReplayAuth struct{}

func (a *policyReplayAuth) AuthenticateHTTP(r *http.Request) (*AuthContext, error) {
	return authFromRequest(r), nil
}

func (a *policyReplayAuth) AuthenticateGRPC(ctx context.Context) (*AuthContext, error) {
	return authFromContext(ctx), nil
}

func (a *policyReplayAuth) RequireRole(r *http.Request, roles ...string) error {
	auth := authFromRequest(r)
	if auth == nil {
		return errors.New("unauthorized")
	}
	role := normalizeRole(auth.Role)
	if role == "" {
		return errors.New("role required")
	}
	for _, candidate := range roles {
		if normalizeRole(candidate) == role {
			return nil
		}
	}
	return errors.New("forbidden")
}

func (a *policyReplayAuth) ResolveTenant(r *http.Request, requested, _ string) (string, error) {
	auth := authFromRequest(r)
	if auth == nil {
		return "", errors.New("unauthorized")
	}
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return strings.TrimSpace(auth.Tenant), nil
	}
	return requested, nil
}

func (a *policyReplayAuth) RequireTenantAccess(r *http.Request, tenant string) error {
	return nil
}

func (a *policyReplayAuth) ResolvePrincipal(r *http.Request, requested string) (string, error) {
	if requested != "" {
		return requested, nil
	}
	auth := authFromRequest(r)
	if auth != nil {
		return auth.PrincipalID, nil
	}
	return "", errors.New("principal required")
}

// seedPolicyBundle stores a policy bundle in configsvc for use by the replay handler.
func seedPolicyBundle(t *testing.T, s *server, bundleID, content string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"content": content,
		"enabled": true,
	})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/policy/bundles/"+bundleID, bytes.NewReader(body))
	req.Header.Set("X-Tenant-ID", "default")
	req.SetPathValue("id", bundleID)
	req = withAuth(req, &AuthContext{Tenant: "default", Role: "admin", PrincipalID: "admin-1"})
	rec := httptest.NewRecorder()
	s.handlePutPolicyBundle(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "seed bundle %s: %s", bundleID, rec.Body.String())
}

// seedTestJobs creates jobs with known metadata and request payloads for replay testing.
func seedTestJobs(t *testing.T, s *server, jobs []testJob) {
	t.Helper()
	ctx := context.Background()
	for _, j := range jobs {
		jobReq := &pb.JobRequest{
			JobId:       j.ID,
			Topic:       j.Topic,
			TenantId:    j.Tenant,
			PrincipalId: "test-principal",
			Meta: &pb.JobMetadata{
				TenantId: j.Tenant,
			},
		}
		err := s.jobStore.SetJobMeta(ctx, jobReq)
		require.NoError(t, err, "set meta for %s", j.ID)

		err = s.jobStore.SetJobRequest(ctx, jobReq)
		require.NoError(t, err, "set request for %s", j.ID)

		err = s.jobStore.SetState(ctx, j.ID, model.JobStatePending)
		require.NoError(t, err, "set state for %s", j.ID)

		if j.SafetyDecision != "" {
			err = s.jobStore.SetSafetyDecision(ctx, j.ID, model.SafetyDecisionRecord{
				Decision: model.SafetyDecision(j.SafetyDecision),
				RuleID:   j.SafetyRuleID,
			})
			require.NoError(t, err, "set safety decision for %s", j.ID)
		}

		// Small sleep to ensure distinct timestamps in the sorted set.
		time.Sleep(5 * time.Millisecond)
	}
}

type testJob struct {
	ID             string
	Topic          string
	Tenant         string
	SafetyDecision string
	SafetyRuleID   string
}

func replayRequest(t *testing.T, s *server, body map[string]any, auth *AuthContext) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/policy/replay", bytes.NewReader(payload))
	req.Header.Set("X-Tenant-ID", "default")
	if auth != nil {
		req = withAuth(req, auth)
	}
	rec := httptest.NewRecorder()
	s.handlePolicyReplay(rec, req)
	return rec
}

func decodeReplayResponse(t *testing.T, rec *httptest.ResponseRecorder) policyReplayResponse {
	t.Helper()
	var resp policyReplayResponse
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err, "decode replay response")
	return resp
}

func TestHandlePolicyReplay_Basic(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = &policyReplayAuth{}

	// Seed a permissive base policy.
	seedPolicyBundle(t, s, "secops/base", `rules:
  - id: allow-all
    match:
      topics:
        - job.*
    decision: allow
`)

	now := time.Now().UTC()
	seedTestJobs(t, s, []testJob{
		{ID: "rp-job-1", Topic: "job.deploy", Tenant: "default", SafetyDecision: "ALLOW"},
		{ID: "rp-job-2", Topic: "job.deploy", Tenant: "default", SafetyDecision: "ALLOW"},
	})

	// Replay with a candidate that denies deploy topics.
	candidateYAML := `rules:
  - id: deny-deploy
    match:
      topics:
        - job.deploy
    decision: deny
    reason: "deploys blocked during freeze"
  - id: allow-all
    match:
      topics:
        - job.*
    decision: allow
`
	rec := replayRequest(t, s, map[string]any{
		"from":               now.Add(-1 * time.Hour).Format(time.RFC3339),
		"to":                 now.Add(1 * time.Hour).Format(time.RFC3339),
		"candidate_content":  candidateYAML,
	}, &AuthContext{Tenant: "default", Role: "admin", PrincipalID: "admin-1"})

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	resp := decodeReplayResponse(t, rec)

	assert.NotEmpty(t, resp.ReplayID)
	assert.NotEmpty(t, resp.PolicySnapshot)
	assert.Equal(t, 2, resp.Summary.TotalJobs)
	assert.Equal(t, 2, resp.Summary.Evaluated)
	assert.Equal(t, 2, resp.Summary.Escalated, "both jobs should be escalated from ALLOW to DENY")
	assert.Equal(t, 0, resp.Summary.Relaxed)
	assert.Equal(t, 0, resp.Summary.Unchanged)
	assert.Len(t, resp.Changes, 2)

	for _, change := range resp.Changes {
		assert.Equal(t, "ALLOW", change.OriginalDecision)
		assert.Equal(t, "DENY", change.NewDecision)
		assert.Equal(t, "escalated", change.Direction)
		assert.Equal(t, "deny-deploy", change.NewRuleID)
	}
}

func TestHandlePolicyReplay_CurrentPolicy(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = &policyReplayAuth{}

	seedPolicyBundle(t, s, "secops/base", `rules:
  - id: allow-all
    match:
      topics:
        - job.*
    decision: allow
`)

	now := time.Now().UTC()
	seedTestJobs(t, s, []testJob{
		{ID: "cp-job-1", Topic: "job.test", Tenant: "default", SafetyDecision: "ALLOW"},
	})

	rec := replayRequest(t, s, map[string]any{
		"from":               now.Add(-1 * time.Hour).Format(time.RFC3339),
		"to":                 now.Add(1 * time.Hour).Format(time.RFC3339),
		"use_current_policy": true,
	}, &AuthContext{Tenant: "default", Role: "admin", PrincipalID: "admin-1"})

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	resp := decodeReplayResponse(t, rec)

	assert.Equal(t, 1, resp.Summary.TotalJobs)
	assert.Equal(t, 1, resp.Summary.Evaluated)
	assert.Equal(t, 1, resp.Summary.Unchanged, "same policy should yield unchanged")
	assert.Equal(t, 0, resp.Summary.Escalated)
}

func TestHandlePolicyReplay_InvalidTimeRange(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = &policyReplayAuth{}
	auth := &AuthContext{Tenant: "default", Role: "admin", PrincipalID: "admin-1"}

	now := time.Now().UTC()

	tests := []struct {
		name string
		body map[string]any
		want string
	}{
		{
			name: "from_after_to",
			body: map[string]any{
				"from":               now.Add(1 * time.Hour).Format(time.RFC3339),
				"to":                 now.Add(-1 * time.Hour).Format(time.RFC3339),
				"use_current_policy": true,
			},
			want: "'from' must be before 'to'",
		},
		{
			name: "span_exceeds_7_days",
			body: map[string]any{
				"from":               now.Add(-8 * 24 * time.Hour).Format(time.RFC3339),
				"to":                 now.Format(time.RFC3339),
				"use_current_policy": true,
			},
			want: "time range exceeds maximum of 7 days",
		},
		{
			name: "invalid_from_format",
			body: map[string]any{
				"from":               "not-a-date",
				"to":                 now.Format(time.RFC3339),
				"use_current_policy": true,
			},
			want: "invalid 'from' timestamp",
		},
		{
			name: "invalid_to_format",
			body: map[string]any{
				"from":               now.Format(time.RFC3339),
				"to":                 "not-a-date",
				"use_current_policy": true,
			},
			want: "invalid 'to' timestamp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := replayRequest(t, s, tt.body, auth)
			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Contains(t, rec.Body.String(), tt.want)
		})
	}
}

func TestHandlePolicyReplay_Filters(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = &policyReplayAuth{}

	seedPolicyBundle(t, s, "secops/base", `rules:
  - id: allow-all
    match:
      topics:
        - job.*
    decision: allow
`)

	now := time.Now().UTC()
	seedTestJobs(t, s, []testJob{
		{ID: "f-job-1", Topic: "job.deploy", Tenant: "tenant-a", SafetyDecision: "ALLOW"},
		{ID: "f-job-2", Topic: "job.build", Tenant: "tenant-b", SafetyDecision: "DENY"},
		{ID: "f-job-3", Topic: "job.deploy", Tenant: "tenant-a", SafetyDecision: "DENY"},
	})

	auth := &AuthContext{Tenant: "default", Role: "admin", PrincipalID: "admin-1"}

	// Filter by tenant.
	rec := replayRequest(t, s, map[string]any{
		"from":               now.Add(-1 * time.Hour).Format(time.RFC3339),
		"to":                 now.Add(1 * time.Hour).Format(time.RFC3339),
		"use_current_policy": true,
		"filters": map[string]any{
			"tenant": "tenant-a",
		},
	}, auth)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	resp := decodeReplayResponse(t, rec)
	assert.Equal(t, 2, resp.Summary.TotalJobs, "should match only tenant-a jobs")

	// Filter by topic pattern.
	rec = replayRequest(t, s, map[string]any{
		"from":               now.Add(-1 * time.Hour).Format(time.RFC3339),
		"to":                 now.Add(1 * time.Hour).Format(time.RFC3339),
		"use_current_policy": true,
		"filters": map[string]any{
			"topic_pattern": "job.deploy",
		},
	}, auth)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	resp = decodeReplayResponse(t, rec)
	assert.Equal(t, 2, resp.Summary.TotalJobs, "should match only deploy jobs")

	// Filter by original decision.
	rec = replayRequest(t, s, map[string]any{
		"from":               now.Add(-1 * time.Hour).Format(time.RFC3339),
		"to":                 now.Add(1 * time.Hour).Format(time.RFC3339),
		"use_current_policy": true,
		"filters": map[string]any{
			"original_decision": "DENY",
		},
	}, auth)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	resp = decodeReplayResponse(t, rec)
	assert.Equal(t, 2, resp.Summary.TotalJobs, "should match only DENY jobs")
}

func TestHandlePolicyReplay_EmptyResult(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = &policyReplayAuth{}

	seedPolicyBundle(t, s, "secops/base", `rules:
  - id: allow-all
    match:
      topics:
        - job.*
    decision: allow
`)

	// Query a time range with no jobs.
	future := time.Now().UTC().Add(10 * time.Hour)
	rec := replayRequest(t, s, map[string]any{
		"from":               future.Format(time.RFC3339),
		"to":                 future.Add(1 * time.Hour).Format(time.RFC3339),
		"use_current_policy": true,
	}, &AuthContext{Tenant: "default", Role: "admin", PrincipalID: "admin-1"})

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	resp := decodeReplayResponse(t, rec)

	assert.Equal(t, 0, resp.Summary.TotalJobs)
	assert.Equal(t, 0, resp.Summary.Evaluated)
	assert.Empty(t, resp.Changes)
}

func TestHandlePolicyReplay_MaxJobs(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = &policyReplayAuth{}

	seedPolicyBundle(t, s, "secops/base", `rules:
  - id: allow-all
    match:
      topics:
        - job.*
    decision: allow
`)

	now := time.Now().UTC()
	jobs := make([]testJob, 10)
	for i := range jobs {
		jobs[i] = testJob{
			ID:             fmt.Sprintf("mx-job-%d", i),
			Topic:          "job.test",
			Tenant:         "default",
			SafetyDecision: "ALLOW",
		}
	}
	seedTestJobs(t, s, jobs)

	// Request max_jobs=3.
	rec := replayRequest(t, s, map[string]any{
		"from":               now.Add(-1 * time.Hour).Format(time.RFC3339),
		"to":                 now.Add(1 * time.Hour).Format(time.RFC3339),
		"use_current_policy": true,
		"max_jobs":           3,
	}, &AuthContext{Tenant: "default", Role: "admin", PrincipalID: "admin-1"})

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	resp := decodeReplayResponse(t, rec)
	assert.Equal(t, 3, resp.Summary.TotalJobs, "should cap at max_jobs=3")

	// Request max_jobs > 1000 should be capped at 1000.
	rec = replayRequest(t, s, map[string]any{
		"from":               now.Add(-1 * time.Hour).Format(time.RFC3339),
		"to":                 now.Add(1 * time.Hour).Format(time.RFC3339),
		"use_current_policy": true,
		"max_jobs":           9999,
	}, &AuthContext{Tenant: "default", Role: "admin", PrincipalID: "admin-1"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	resp = decodeReplayResponse(t, rec)
	assert.LessOrEqual(t, resp.Summary.TotalJobs, 1000, "should be capped at 1000")
}

func TestHandlePolicyReplay_Auth(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = &policyReplayAuth{}

	now := time.Now().UTC()
	body := map[string]any{
		"from":               now.Add(-1 * time.Hour).Format(time.RFC3339),
		"to":                 now.Add(1 * time.Hour).Format(time.RFC3339),
		"use_current_policy": true,
	}

	// No auth context — should fail.
	rec := replayRequest(t, s, body, nil)
	assert.Equal(t, http.StatusForbidden, rec.Code)

	// Viewer role — should fail.
	rec = replayRequest(t, s, body, &AuthContext{Tenant: "default", Role: "viewer", PrincipalID: "viewer-1"})
	assert.Equal(t, http.StatusForbidden, rec.Code)

	// Admin role — should succeed (even without seeded bundles/jobs).
	rec = replayRequest(t, s, body, &AuthContext{Tenant: "default", Role: "admin", PrincipalID: "admin-1"})
	assert.Equal(t, http.StatusOK, rec.Code, "admin should be allowed: %s", rec.Body.String())
}

func TestHandlePolicyReplay_VelocityWarning(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = &policyReplayAuth{}

	now := time.Now().UTC()
	candidateYAML := `rules:
  - id: rate-limit
    match:
      topics:
        - job.*
    velocity:
      max_requests: 10
      window_seconds: 60
      key: tenant
    decision: throttle
    reason: "rate limited"
  - id: allow-all
    match:
      topics:
        - job.*
    decision: allow
`

	rec := replayRequest(t, s, map[string]any{
		"from":              now.Add(-1 * time.Hour).Format(time.RFC3339),
		"to":                now.Add(1 * time.Hour).Format(time.RFC3339),
		"candidate_content": candidateYAML,
	}, &AuthContext{Tenant: "default", Role: "admin", PrincipalID: "admin-1"})

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	resp := decodeReplayResponse(t, rec)
	assert.Contains(t, resp.Warnings, "Velocity rules are not replayed (they depend on time-windowed counters)")
}

func TestHandlePolicyReplay_NoPolicySpecified(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = &policyReplayAuth{}

	now := time.Now().UTC()
	rec := replayRequest(t, s, map[string]any{
		"from": now.Add(-1 * time.Hour).Format(time.RFC3339),
		"to":   now.Add(1 * time.Hour).Format(time.RFC3339),
	}, &AuthContext{Tenant: "default", Role: "admin", PrincipalID: "admin-1"})

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "one of candidate_content")
}
