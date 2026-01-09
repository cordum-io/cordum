package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/infra/memory"
)

func TestHandleGetMemoryRedisTypes(t *testing.T) {
	s, _, _ := newTestGateway(t)
	store, ok := s.memStore.(*memory.RedisStore)
	if !ok {
		t.Fatalf("expected redis store")
	}
	client := store.Client()
	ctx := context.Background()

	stringKey := "mem:val:string"
	if err := client.Set(ctx, stringKey, `{"k":"v"}`, 0).Err(); err != nil {
		t.Fatalf("set string: %v", err)
	}
	listKey := "mem:val:list"
	if err := client.RPush(ctx, listKey, `"a"`, `"b"`).Err(); err != nil {
		t.Fatalf("set list: %v", err)
	}
	setKey := "mem:val:set"
	if err := client.SAdd(ctx, setKey, `"x"`, `"y"`).Err(); err != nil {
		t.Fatalf("set set: %v", err)
	}
	hashKey := "mem:val:hash"
	if err := client.HSet(ctx, hashKey, "field", `"v"`).Err(); err != nil {
		t.Fatalf("set hash: %v", err)
	}

	assertMemory := func(key string, expectedType string) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/memory?key="+key, nil)
		rec := httptest.NewRecorder()
		s.handleGetMemory(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("memory %s status: %d", key, rec.Code)
		}
		var resp map[string]any
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode memory response: %v", err)
		}
		if resp["kind"] != "memory" {
			t.Fatalf("expected memory kind for %s", key)
		}
		jsonVal, ok := resp["json"].(map[string]any)
		if !ok {
			t.Fatalf("expected json payload for %s", key)
		}
		if jsonVal["redis_type"] != expectedType {
			t.Fatalf("expected redis_type %s for %s", expectedType, key)
		}
	}

	assertMemory(stringKey, "string")
	assertMemory(listKey, "list")
	assertMemory(setKey, "set")
	assertMemory(hashKey, "hash")
}

func TestHandleGetTrace(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()
	jobID := "job-trace"
	traceID := "trace-1"
	if err := s.jobStore.SetState(ctx, jobID, scheduler.JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}
	if err := s.jobStore.AddJobToTrace(ctx, traceID, jobID); err != nil {
		t.Fatalf("add trace: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/traces/"+traceID, nil)
	req.SetPathValue("id", traceID)
	rec := httptest.NewRecorder()
	s.handleGetTrace(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("trace status: %d", rec.Code)
	}
	var jobs []map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&jobs); err != nil {
		t.Fatalf("decode trace: %v", err)
	}
	if len(jobs) != 1 || jobs[0]["id"] != jobID {
		t.Fatalf("unexpected trace jobs: %#v", jobs)
	}
}
