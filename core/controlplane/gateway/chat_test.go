package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	wf "github.com/cordum/cordum/core/workflow"
)

func TestRunChatHandlers(t *testing.T) {
	s, _, _ := newTestGateway(t)

	wfDef := &wf.Workflow{
		ID:    "wf-chat",
		OrgID: "default",
		Steps: map[string]*wf.Step{
			"step": {ID: "step", Type: wf.StepTypeWorker, Topic: "job.test"},
		},
	}
	if err := s.workflowStore.SaveWorkflow(context.Background(), wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	run := &wf.WorkflowRun{
		ID:         "run-chat-1",
		WorkflowID: wfDef.ID,
		OrgID:      "default",
		Status:     wf.RunStatusRunning,
		Input:      map[string]any{"memory_id": "chat:demo"},
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := s.workflowStore.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	postBody, _ := json.Marshal(map[string]any{"content": "hello chat"})
	postReq := httptest.NewRequest(http.MethodPost, "/api/v1/workflow-runs/"+run.ID+"/chat", bytes.NewReader(postBody))
	postReq.Header.Set("X-Tenant-ID", "default")
	postReq.SetPathValue("id", run.ID)
	postRec := httptest.NewRecorder()
	s.handlePostRunChat(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("post chat: %d %s", postRec.Code, postRec.Body.String())
	}
	var postResp struct {
		ID      string `json:"id"`
		RunID   string `json:"run_id"`
		Content string `json:"content"`
		Role    string `json:"role"`
	}
	if err := json.NewDecoder(postRec.Body).Decode(&postResp); err != nil {
		t.Fatalf("decode post response: %v", err)
	}
	if postResp.ID == "" || postResp.RunID != run.ID || postResp.Content != "hello chat" || postResp.Role == "" {
		t.Fatalf("unexpected post response: %+v", postResp)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/workflow-runs/"+run.ID+"/chat", nil)
	getReq.Header.Set("X-Tenant-ID", "default")
	getReq.SetPathValue("id", run.ID)
	getRec := httptest.NewRecorder()
	s.handleGetRunChat(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get chat: %d %s", getRec.Code, getRec.Body.String())
	}
	var getResp struct {
		Items []struct {
			Content string `json:"content"`
		} `json:"items"`
	}
	if err := json.NewDecoder(getRec.Body).Decode(&getResp); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	if len(getResp.Items) == 0 || getResp.Items[len(getResp.Items)-1].Content != "hello chat" {
		t.Fatalf("unexpected chat history: %+v", getResp.Items)
	}
}
