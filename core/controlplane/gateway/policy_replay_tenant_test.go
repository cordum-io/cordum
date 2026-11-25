package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPolicyReplayTenantBound(t *testing.T) {
	for _, name := range []string{"unfiltered", "filtered-to-b"} {
		t.Run(name, func(t *testing.T) {
			sw := newCrossTenantSweep(t)
			id, marker := seedCrossTenantJob(t, sw)
			before := crossTenantJobMeta(t, sw, id)
			filter := ""
			if name == "filtered-to-b" {
				filter = sw.tenantB.tenantID
			}
			rr := sw.as(t, sw.tenantA, http.MethodPost, "/api/v1/policy/replay", replayTenantBody(t, filter))
			resp := replayTenantResponse(t, rr)
			assert.Equal(t, policyReplaySummary{}, resp.Summary)
			assert.Empty(t, resp.RuleHits)
			assert.Empty(t, resp.Changes)
			assert.Empty(t, resp.Errors)
			assert.NotContains(t, rr.Body.String(), id)
			assert.NotContains(t, rr.Body.String(), marker)
			assert.Equal(t, before, crossTenantJobMeta(t, sw, id))
		})
	}
	t.Run("owner", replayTenantOwner)
	t.Run("all-tenants", replayTenantAll)
	t.Run("tenantless", replayTenantMissing)
}

func replayTenantBody(t *testing.T, tenant string) string {
	t.Helper()
	now := time.Now().UTC()
	body := policyReplayRequest{
		From: now.Add(-time.Hour).Format(time.RFC3339),
		To:   now.Add(time.Hour).Format(time.RFC3339),
		CandidateContent: `rules:
  - id: deny-jobs
    match:
      topics:
        - job.*
    decision: deny
`,
	}
	if tenant != "" {
		body.Filters = &policyReplayFilter{Tenant: tenant}
	}
	payload, err := json.Marshal(body)
	require.NoError(t, err)
	return string(payload)
}

func replayTenantResponse(t *testing.T, rr *httptest.ResponseRecorder) policyReplayResponse {
	t.Helper()
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var resp policyReplayResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	return resp
}

func replayTenantOwner(t *testing.T) {
	sw := newCrossTenantSweep(t)
	id, marker := seedCrossTenantJob(t, sw)
	for _, filter := range []string{"", sw.tenantB.tenantID} {
		rr := sw.as(t, sw.tenantB, http.MethodPost, "/api/v1/policy/replay", replayTenantBody(t, filter))
		resp := replayTenantResponse(t, rr)
		assert.Equal(t, 1, resp.Summary.TotalJobs)
		assert.Equal(t, 1, resp.Summary.Evaluated)
		require.Len(t, resp.Changes, 1)
		assert.Equal(t, id, resp.Changes[0].JobID)
		assert.Equal(t, "job."+marker, resp.Changes[0].Topic)
	}
}

func replayTenantAll(t *testing.T) {
	sw := newCrossTenantSweep(t)
	id, _ := seedCrossTenantJob(t, sw)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/policy/replay", strings.NewReader(replayTenantBody(t, "")))
	req = withAuth(req, &auth.AuthContext{Tenant: sw.tenantA.tenantID, Role: "admin", AllowCrossTenant: true})
	rr := httptest.NewRecorder()
	sw.server.handlePolicyReplay(rr, req)
	resp := replayTenantResponse(t, rr)
	assert.Equal(t, 1, resp.Summary.TotalJobs)
	assert.Equal(t, 1, resp.Summary.Evaluated)
	require.Len(t, resp.Changes, 1)
	assert.Equal(t, id, resp.Changes[0].JobID)
}

func replayTenantMissing(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = &policyReplayAuth{}
	seedTestJobs(t, s, []testJob{
		{ID: "replay-owned", Topic: "job.owned", Tenant: "default", SafetyDecision: "ALLOW"},
		{ID: "replay-tenantless", Topic: "job.tenantless", Tenant: "", SafetyDecision: "ALLOW"},
	})
	for _, allowAll := range []bool{false, true} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/policy/replay", strings.NewReader(replayTenantBody(t, "")))
		req = withAuth(req, &auth.AuthContext{Tenant: "default", Role: "admin", AllowCrossTenant: allowAll})
		rr := httptest.NewRecorder()
		s.handlePolicyReplay(rr, req)
		resp := replayTenantResponse(t, rr)
		if allowAll {
			assert.Equal(t, 2, resp.Summary.TotalJobs)
		} else {
			assert.Equal(t, 1, resp.Summary.TotalJobs)
			assert.NotContains(t, rr.Body.String(), "replay-tenantless")
			assert.NotContains(t, rr.Body.String(), "job.tenantless")
		}
	}
}
