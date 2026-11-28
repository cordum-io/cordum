package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/cordum/cordum/core/infra/config"
)

func TestTopicSubmitRejectedAfterLastRegistrationDeleted(t *testing.T) {
	sw := newCrossTenantSweep(t)
	sw.server.tenant = "topic-mapping-platform"
	topicMappingRegister(t, sw, sw.tenantB)
	seedPoolConfig(t, sw.server, map[string]any{"job.topic-mapping": "mapping-pool"},
		map[string]config.PoolConfig{"mapping-pool": {}})
	if rr := sw.as(t, sw.tenantB, http.MethodDelete, "/api/v1/topics/job.topic-mapping", ""); rr.Code != http.StatusNoContent {
		t.Fatalf("delete topic: status=%d body=%s", rr.Code, rr.Body.String())
	}
	ctx := context.Background()
	jobsBefore, err := sw.server.jobStore.Client().Keys(ctx, "job:*").Result()
	if err != nil {
		t.Fatal(err)
	}
	mark := sw.publishMark()

	body := fmt.Sprintf(`{"prompt":"stored-registry","topic":"job.topic-mapping","org_id":%q}`, sw.tenantA.tenantID)
	rr := sw.as(t, sw.tenantA, http.MethodPost, "/api/v1/jobs", body)
	var resp struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if rr.Code != http.StatusBadRequest || resp.Code != "unknown_topic" {
		t.Errorf("submit after last registration deleted: status=%d body=%s, want 400 unknown_topic", rr.Code, rr.Body.String())
	}
	if got := sw.publishedSince(mark); len(got) != 0 {
		t.Errorf("published %d messages, want none: %v", len(got), got)
	}
	jobsAfter, err := sw.server.jobStore.Client().Keys(ctx, "job:*").Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(jobsAfter) != len(jobsBefore) {
		t.Errorf("job keys before=%v after=%v, want no job stored", jobsBefore, jobsAfter)
	}
	if doc := topicMappingDocument(t, sw, "topics"); len(doc.Data) != 0 {
		t.Errorf("system/topics records=%v, want none", doc.Data)
	}
}
