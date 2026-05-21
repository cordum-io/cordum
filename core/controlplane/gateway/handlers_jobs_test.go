package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cordum/cordum/core/controlplane/topicregistry"
	infraSchema "github.com/cordum/cordum/core/infra/schema"
)

func TestJobs_400EmitsCodeField(t *testing.T) {
	t.Run("unknown_topic", func(t *testing.T) {
		s, _, _ := newTestGateway(t)
		seedJobTopic(t, s, "job.allowed", topicregistry.StatusActive)

		resp := submitJobBadRequest(t, s, map[string]any{
			"prompt": "hello",
			"topic":  "job.unknown",
		})

		assertJobErrorCodeField(t, resp, "unknown_topic")
	})

	t.Run("topic_disabled", func(t *testing.T) {
		s, _, _ := newTestGateway(t)
		seedJobTopic(t, s, "job.disabled", topicregistry.StatusDisabled)

		resp := submitJobBadRequest(t, s, map[string]any{
			"prompt": "hello",
			"topic":  "job.disabled",
		})

		assertJobErrorCodeField(t, resp, "topic_disabled")
	})

	t.Run("schema_validation_failed", func(t *testing.T) {
		s, _, _ := newTestGateway(t)
		s.schemaEnforcement = infraSchema.EnforcementEnforce
		registerSubmitSchemaTopic(t, s, "job.structured", "demo/input")

		resp := submitJobBadRequest(t, s, map[string]any{
			"prompt": "hello",
			"topic":  "job.structured",
			"context": map[string]any{
				"message": 123,
			},
		})

		assertJobErrorCodeField(t, resp, "schema_validation_failed")
	})
}

func seedJobTopic(t *testing.T, s *server, name, status string) {
	t.Helper()
	if err := s.topicRegistry.Set(context.Background(), topicregistry.Registration{
		Name:   name,
		Pool:   "pool-a",
		Status: status,
	}); err != nil {
		t.Fatalf("seed topic %s: %v", name, err)
	}
}

func submitJobBadRequest(t *testing.T, s *server, payload map[string]any) map[string]any {
	t.Helper()
	s.tenant = "default"
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", bytes.NewReader(raw))
	req.Header.Set("X-Tenant-ID", "default")
	rec := httptest.NewRecorder()
	s.handleSubmitJobHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}

func assertJobErrorCodeField(t *testing.T, resp map[string]any, want string) {
	t.Helper()
	if legacy, ok := resp["error_code"]; ok {
		t.Fatalf("response still emits legacy error_code=%#v; body=%#v", legacy, resp)
	}
	if got := resp["code"]; got != want {
		t.Fatalf("code = %#v, want %q; body=%#v", got, want, resp)
	}
}
