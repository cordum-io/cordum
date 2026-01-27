package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
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

	postBody2, _ := json.Marshal(map[string]any{"content": "second chat"})
	postReq2 := httptest.NewRequest(http.MethodPost, "/api/v1/workflow-runs/"+run.ID+"/chat", bytes.NewReader(postBody2))
	postReq2.Header.Set("X-Tenant-ID", "default")
	postReq2.SetPathValue("id", run.ID)
	postRec2 := httptest.NewRecorder()
	s.handlePostRunChat(postRec2, postReq2)
	if postRec2.Code != http.StatusOK {
		t.Fatalf("post chat 2: %d %s", postRec2.Code, postRec2.Body.String())
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

	cursorReq := httptest.NewRequest(http.MethodGet, "/api/v1/workflow-runs/"+run.ID+"/chat?limit=1", nil)
	cursorReq.Header.Set("X-Tenant-ID", "default")
	cursorReq.SetPathValue("id", run.ID)
	cursorRec := httptest.NewRecorder()
	s.handleGetRunChat(cursorRec, cursorReq)
	if cursorRec.Code != http.StatusOK {
		t.Fatalf("get chat cursor: %d %s", cursorRec.Code, cursorRec.Body.String())
	}
	var cursorResp struct {
		Items []struct {
			Content string `json:"content"`
		} `json:"items"`
		NextCursor *int64 `json:"next_cursor"`
	}
	if err := json.NewDecoder(cursorRec.Body).Decode(&cursorResp); err != nil {
		t.Fatalf("decode cursor response: %v", err)
	}
	if len(cursorResp.Items) != 1 || cursorResp.Items[0].Content != "second chat" {
		t.Fatalf("unexpected cursor page: %+v", cursorResp.Items)
	}
	if cursorResp.NextCursor == nil {
		t.Fatalf("expected next cursor")
	}

	olderReq := httptest.NewRequest(http.MethodGet, "/api/v1/workflow-runs/"+run.ID+"/chat?limit=1&cursor="+strconv.FormatInt(*cursorResp.NextCursor, 10), nil)
	olderReq.Header.Set("X-Tenant-ID", "default")
	olderReq.SetPathValue("id", run.ID)
	olderRec := httptest.NewRecorder()
	s.handleGetRunChat(olderRec, olderReq)
	if olderRec.Code != http.StatusOK {
		t.Fatalf("get older chat: %d %s", olderRec.Code, olderRec.Body.String())
	}
	var olderResp struct {
		Items []struct {
			Content string `json:"content"`
		} `json:"items"`
	}
	if err := json.NewDecoder(olderRec.Body).Decode(&olderResp); err != nil {
		t.Fatalf("decode older response: %v", err)
	}
	if len(olderResp.Items) != 1 || olderResp.Items[0].Content != "hello chat" {
		t.Fatalf("unexpected older page: %+v", olderResp.Items)
	}
}
