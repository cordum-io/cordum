package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	"github.com/cordum/cordum/tests/identity"
)

// TestAudienceMatrixHTTPAPI presents every credential to POST /api/v1/jobs.
// The protected effect is a stored job (job:state:*) plus its sys.job.submit
// publish.
func TestAudienceMatrixHTTPAPI(t *testing.T) {
	m := newAudienceMatrix(t)
	identity.Run(t, identity.HTTPAPI, func(t *testing.T, c identity.Credential) identity.Result {
		body := map[string]string{"prompt": "audience matrix", "topic": "job.test"}
		if c == identity.DelegationToken {
			body["delegation_token"] = m.tokens[c] // the job body is where this entry reads it
		}
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		newReq := func() *http.Request { return matrixRequest("/api/v1/jobs", string(raw)) }
		return m.presentHTTP(t, identity.HTTPAPI, c, newReq, m.jobEffects, "job:state keys + sys.job.submit publishes")
	})
}

// TestAudienceMatrixEdgeAPI presents every credential to POST
// /api/v1/edge/sessions. The protected effect is a new Edge session record
// (edge:session:<id>) in any tenant.
func TestAudienceMatrixEdgeAPI(t *testing.T) {
	m := newAudienceMatrix(t)
	identity.Run(t, identity.EdgeAPI, func(t *testing.T, c identity.Credential) identity.Result {
		newReq := func() *http.Request {
			return matrixRequest("/api/v1/edge/sessions", `{"agent_product":"claude-code","mode":"local-dev"}`)
		}
		return m.presentHTTP(t, identity.EdgeAPI, c, newReq, m.edgeSessions, "edge:session records")
	})
}

func matrixRequest(path, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", matrixTenant)
	return req
}

func (m *audienceMatrix) jobEffects(t *testing.T) int {
	t.Helper()
	keys, err := m.s.jobStore.Client().Keys(context.Background(), "job:state:*").Result()
	if err != nil {
		t.Fatalf("job:state keys: %v", err)
	}
	m.bus.mu.Lock()
	defer m.bus.mu.Unlock()
	n := len(keys)
	for _, p := range m.bus.published {
		if p.subject == capsdk.SubjectSubmit {
			n++
		}
	}
	return n
}

func (m *audienceMatrix) edgeSessions(t *testing.T) int {
	t.Helper()
	keys, err := m.s.jobStore.Client().Keys(context.Background(), "edge:session:*").Result()
	if err != nil {
		t.Fatalf("edge session records: %v", err)
	}
	n := 0
	for _, k := range keys {
		if !strings.HasPrefix(k, "edge:session:heartbeat:") {
			n++
		}
	}
	return n
}
