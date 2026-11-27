package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// A submit whose key matches a legacy unscoped key, whose job's tenant cannot
// be read, is handled as a new job under the caller's tenant-scoped key.
func TestTenantIsolationGatewaySubmitLegacyIdempotencyKey(t *testing.T) {
	const key = "legacy-owner-unknown"
	const legacyKey = "job:idempotency:" + key
	for _, tc := range []struct {
		name   string
		submit func(t *testing.T, s *server) string
	}{
		{"http", func(t *testing.T, s *server) string {
			body, _ := json.Marshal(map[string]any{"prompt": "hello", "topic": "job.test", "idempotency_key": key})
			req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", bytes.NewReader(body))
			req.Header.Set("X-Tenant-ID", "default")
			rec := httptest.NewRecorder()
			s.handleSubmitJobHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("submit: status %d: %s", rec.Code, rec.Body.String())
			}
			var resp map[string]string
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			return resp["job_id"]
		}},
		{"grpc", func(t *testing.T, s *server) string {
			resp, err := s.SubmitJob(context.Background(), &pb.SubmitJobRequest{
				Prompt: "hello", Topic: "job.default", IdempotencyKey: key,
			})
			if err != nil {
				t.Fatalf("submit: %v", err)
			}
			return resp.JobId
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, bus, _ := newTestGateway(t)
			s.tenant = "default"
			ctx := context.Background()
			client := s.jobStore.Client()
			if err := client.Set(ctx, legacyKey, "job-legacy", 0).Err(); err != nil {
				t.Fatalf("seed legacy key: %v", err)
			}

			jobID := tc.submit(t, s)
			if jobID == "" || jobID == "job-legacy" {
				t.Fatalf("job_id = %q; want a new job", jobID)
			}
			if len(bus.published) != 1 {
				t.Fatalf("bus publishes = %d; want 1", len(bus.published))
			}
			if got, err := client.Get(ctx, "job:idempotency:default:"+key).Result(); err != nil || got != jobID {
				t.Fatalf("scoped key = %q, %v; want %q", got, err, jobID)
			}
			if got, err := client.Get(ctx, legacyKey).Result(); err != nil || got != "job-legacy" {
				t.Fatalf("legacy key = %q, %v; want job-legacy", got, err)
			}
		})
	}
}
