package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	wf "github.com/cordum/cordum/core/workflow"
)

const runChatMarker = "TENANT-B-RUN-CHAT"

func runChatRequest(method, tenant, runID, content string) *http.Request {
	r := tenantObjectRequest(method, "/api/v1/workflow-runs/"+runID+"/chat", tenant, runID)
	if content != "" {
		body, _ := json.Marshal(map[string]string{"content": content})
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
	}
	return r
}

func seedTenantRun(t *testing.T, s *server, tenant, runID string, input map[string]any) {
	t.Helper()
	ctx := context.Background()
	wfID := "wf-" + runID
	if err := s.workflowStore.SaveWorkflow(ctx, &wf.Workflow{ID: wfID, OrgID: tenant, Name: "chat"}); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	run := &wf.WorkflowRun{ID: runID, WorkflowID: wfID, OrgID: tenant, Status: wf.RunStatusRunning, Input: input}
	if err := s.workflowStore.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
}

func TestTenantIsolationGatewayRunChatMemory(t *testing.T) {
	for _, tc := range []struct {
		name, memoryID string
		wantPost       int
		wantKey        string
	}{
		{"unqualified-id", "run:run-chat-b", http.StatusOK, "mem:tenant-a/run:run-chat-b:events"},
		{"foreign-qualified-id", "tenant-b/run:run-chat-b", http.StatusBadRequest, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _ := newTestGateway(t)
			s.auth = newBasicAuthForTest(t, nil)
			client, err := chatRedisClient(s.memStore)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()

			// Control: B's own run chat is stored under B's qualified run memory id.
			seedTenantRun(t, s, "tenant-b", "run-chat-b", nil)
			rec := httptest.NewRecorder()
			s.handlePostRunChat(rec, runChatRequest(http.MethodPost, "tenant-b", "run-chat-b", runChatMarker))
			if rec.Code != http.StatusOK {
				t.Fatalf("B post: %d %s", rec.Code, rec.Body.String())
			}
			rec = httptest.NewRecorder()
			s.handleGetRunChat(rec, runChatRequest(http.MethodGet, "tenant-b", "run-chat-b", ""))
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), runChatMarker) {
				t.Fatalf("B get: %d %s", rec.Code, rec.Body.String())
			}
			bKey := "mem:tenant-b/run:run-chat-b:events"
			before, err := client.LRange(ctx, bKey, 0, -1).Result()
			if err != nil || len(before) != 1 || !strings.Contains(before[0], runChatMarker) {
				t.Fatalf("B history at %s: %v, %v", bKey, before, err)
			}

			// A's own run names B's memory id.
			seedTenantRun(t, s, "tenant-a", "run-chat-a", map[string]any{"memory_id": tc.memoryID})
			snapshot := tenantObjectSnapshot(t, s)
			rec = httptest.NewRecorder()
			s.handlePostRunChat(rec, runChatRequest(http.MethodPost, "tenant-a", "run-chat-a", "tenant A message"))
			if rec.Code != tc.wantPost {
				t.Errorf("A post: %d %s, want %d", rec.Code, rec.Body.String(), tc.wantPost)
			}
			written := tenantObjectSnapshot(t, s)
			for key, value := range snapshot {
				if written[key] != value {
					t.Errorf("A post changed %s", key)
				}
			}
			for key := range written {
				if _, ok := snapshot[key]; !ok && key != tc.wantKey {
					t.Errorf("A post wrote %s", key)
				}
			}
			if tc.wantKey != "" && !strings.Contains(written[tc.wantKey], "tenant A message") {
				t.Errorf("A history at %s: %q", tc.wantKey, written[tc.wantKey])
			}
			rec = httptest.NewRecorder()
			s.handleGetRunChat(rec, runChatRequest(http.MethodGet, "tenant-a", "run-chat-a", ""))
			if strings.Contains(rec.Body.String(), runChatMarker) {
				t.Errorf("A get returned B's history: %s", rec.Body.String())
			}
			after, err := client.LRange(ctx, bKey, 0, -1).Result()
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Errorf("B history changed: %v, %v", after, err)
			}
		})
	}
}
