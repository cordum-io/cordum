package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/mcp"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// These tests drive the HTTP MCP transport after the production
// registration sequence (registerMCPRoutes), with the policy gate off as
// in the governed demo and every request authenticated by a real
// BasicAuthProvider. They pin the job read through cordum://jobs/{id},
// the cordum_get_job and cordum_status tools, and the demo worker's
// open-then-create sequence.

const (
	mcpJobReadKey          = "mcpjobread-worker-admin-key-0000000001"
	mcpJobReadNarrowKey    = "mcpjobread-narrow-admin-key-0000000001"
	mcpJobReadPrincipal    = "mcpjobread-worker"
	mcpJobReadNarrowPrinc  = "mcpjobread-narrow"
	mcpJobReadAgent        = "agent-mcpjobread"
	mcpJobReadNarrowAgent  = "agent-mcpjobread-narrow"
	mcpJobReadJob          = "job-mcpjobread-1"
	mcpJobReadOtherJob     = "job-mcpjobread-other"
	mcpJobReadOtherTopic   = "job.mcpjobread.other-tenant-marker"
	mcpJobReadJobsTemplate = "cordum://jobs/{id}"
)

type mcpJobReadResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	} `json:"error"`
	raw string
}

// mcpJobReadServer serves the HTTP MCP routes for two static admin keys in
// tenant "default". mcpJobReadKey's principal is linked to an agent allowed
// cordum_status, cordum_get_job and cordum_create_workflow;
// mcpJobReadNarrowKey's agent is allowed cordum_status only. It seeds a
// pending job in "default" and one in tenant "other".
func mcpJobReadServer(t *testing.T) (*server, *httptest.Server) {
	t.Helper()
	s, _, _ := newTestGateway(t)
	setMCPConfigForBoot(t, s, true, false)
	ctx := context.Background()
	agents := []struct {
		id, principal string
		tools         []string
	}{
		{mcpJobReadAgent, mcpJobReadPrincipal, []string{mcp.ToolStatus, mcp.ToolGetJob, mcp.ToolCreateWorkflow}},
		{mcpJobReadNarrowAgent, mcpJobReadNarrowPrinc, []string{mcp.ToolStatus}},
	}
	for _, a := range agents {
		if _, err := s.agentIdentityStore.Create(ctx, store.AgentIdentity{
			ID: a.id, TenantID: "default", Name: a.id, Owner: "alice@example.com",
			RiskTier: "high", AllowedTools: a.tools, Status: "active",
		}); err != nil {
			t.Fatalf("create agent identity %s: %v", a.id, err)
		}
		if err := s.agentIdentityStore.LinkWorker(ctx, a.id, a.principal); err != nil {
			t.Fatalf("LinkWorker %s: %v", a.id, err)
		}
	}
	keys, err := json.Marshal([]map[string]string{
		{"key": mcpJobReadKey, "tenant": "default", "role": "admin", "principal_id": mcpJobReadPrincipal},
		{"key": mcpJobReadNarrowKey, "tenant": "default", "role": "admin", "principal_id": mcpJobReadNarrowPrinc},
	})
	if err != nil {
		t.Fatalf("marshal api keys: %v", err)
	}
	s.auth = newBasicAuthForTest(t, map[string]string{"CORDUM_API_KEYS": string(keys)})
	jobs := []struct{ id, tenant, topic string }{
		{mcpJobReadJob, "default", "job.mcpjobread.visible"},
		{mcpJobReadOtherJob, "other", mcpJobReadOtherTopic},
	}
	for _, j := range jobs {
		req := &pb.JobRequest{JobId: j.id, Topic: j.topic, TenantId: j.tenant, PrincipalId: mcpJobReadPrincipal}
		if err := s.jobStore.SetJobMeta(ctx, req); err != nil {
			t.Fatalf("SetJobMeta %s: %v", j.id, err)
		}
		if err := s.jobStore.SetState(ctx, j.id, model.JobStatePending); err != nil {
			t.Fatalf("SetState %s: %v", j.id, err)
		}
	}
	s.shutdownCh = make(chan struct{})
	t.Cleanup(func() { close(s.shutdownCh) })
	mux := http.NewServeMux()
	if err := s.registerMCPRoutes(mux); err != nil {
		t.Fatalf("register mcp routes: %v", err)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return s, srv
}

// mcpJobReadPost posts one JSON-RPC message to /mcp/message with apiKey and
// returns the HTTP status and body.
func mcpJobReadPost(t *testing.T, baseURL, apiKey, body string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, baseURL+"/mcp/message", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	req.Header.Set("X-Tenant-ID", "default")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post %s: %v", body, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return resp.StatusCode, raw
}

// mcpJobReadRPC sends one JSON-RPC request and decodes the 200 response.
func mcpJobReadRPC(t *testing.T, baseURL, apiKey, method, params string) mcpJobReadResponse {
	t.Helper()
	code, raw := mcpJobReadPost(t, baseURL, apiKey, `{"jsonrpc":"2.0","id":1,"method":"`+method+`","params":`+params+`}`)
	if code != http.StatusOK {
		t.Fatalf("%s: HTTP %d, want 200; body=%s", method, code, raw)
	}
	out := mcpJobReadResponse{raw: string(raw)}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode %s response: %v body=%s", method, err, raw)
	}
	if (out.Error == nil) == (len(out.Result) == 0 || string(out.Result) == "null") {
		t.Fatalf("%s: want exactly one of result and error; body=%s", method, raw)
	}
	return out
}

// mcpJobReadItem reads uri and decodes its one JSON content item.
func mcpJobReadItem(t *testing.T, baseURL, apiKey, uri string) mcp.ResourceItem {
	t.Helper()
	resp := mcpJobReadRPC(t, baseURL, apiKey, mcp.MethodResourcesRead, `{"uri":"`+uri+`"}`)
	if resp.Error != nil {
		t.Fatalf("read %s: error %+v, want contents; body=%s", uri, resp.Error, resp.raw)
	}
	var read mcp.ResourceReadResult
	if err := json.Unmarshal(resp.Result, &read); err != nil || len(read.Contents) != 1 {
		t.Fatalf("read %s: contents=%d err=%v, want 1; body=%s", uri, len(read.Contents), err, resp.raw)
	}
	if got := read.Contents[0]; got.URI != uri || got.MIMEType != "application/json" {
		t.Fatalf("read %s: content uri=%q mime=%q", uri, got.URI, got.MIMEType)
	}
	var item mcp.ResourceItem
	if err := json.Unmarshal([]byte(read.Contents[0].Text), &item); err != nil {
		t.Fatalf("decode %s item: %v text=%s", uri, err, read.Contents[0].Text)
	}
	return item
}

// mcpJobReadToolItem calls a read-only tool and decodes its one JSON text
// content into a ResourceItem.
func mcpJobReadToolItem(t *testing.T, baseURL, apiKey, tool, args string) mcp.ResourceItem {
	t.Helper()
	resp := mcpJobReadRPC(t, baseURL, apiKey, "tools/call", `{"name":"`+tool+`","arguments":`+args+`}`)
	if resp.Error != nil {
		t.Fatalf("%s: error %+v, want a result; body=%s", tool, resp.Error, resp.raw)
	}
	var result mcp.ToolCallResult
	if err := json.Unmarshal(resp.Result, &result); err != nil || result.IsError || len(result.Content) != 1 {
		t.Fatalf("%s: err=%v isError=%v content=%d; body=%s", tool, err, result.IsError, len(result.Content), resp.raw)
	}
	var item mcp.ResourceItem
	if err := json.Unmarshal([]byte(result.Content[0].Text), &item); err != nil {
		t.Fatalf("decode %s item: %v text=%s", tool, err, result.Content[0].Text)
	}
	return item
}

// mcpJobReadRequireJob checks item is the seeded tenant-visible job.
func mcpJobReadRequireJob(t *testing.T, item mcp.ResourceItem) {
	t.Helper()
	if item.ID != mcpJobReadJob || item.Kind != "job" {
		t.Fatalf("item id=%q kind=%q, want %s/job", item.ID, item.Kind, mcpJobReadJob)
	}
	if item.Data["id"] != mcpJobReadJob || item.Data["tenant"] != "default" || item.Data["state"] != string(model.JobStatePending) {
		t.Fatalf("item data id=%v tenant=%v state=%v, want %s/default/%s",
			item.Data["id"], item.Data["tenant"], item.Data["state"], mcpJobReadJob, model.JobStatePending)
	}
}

// mcpJobReadRequireFailed checks resp is a JSON-RPC error that carries no
// job record.
func mcpJobReadRequireFailed(t *testing.T, resp mcpJobReadResponse, what string) {
	t.Helper()
	if resp.Error == nil || len(resp.Result) != 0 {
		t.Fatalf("%s: want a JSON-RPC error and no result; body=%s", what, resp.raw)
	}
	for _, leak := range []string{mcpJobReadOtherTopic, `"kind":"job"`, `\"kind\":\"job\"`} {
		if strings.Contains(resp.raw, leak) {
			t.Fatalf("%s: response carries %s; body=%s", what, leak, resp.raw)
		}
	}
}

func TestMCPJobRead_ProductionRegistration(t *testing.T) {
	_, srv := mcpJobReadServer(t)

	t.Run("jobs template registered once", func(t *testing.T) {
		resp := mcpJobReadRPC(t, srv.URL, mcpJobReadKey, mcp.MethodResourceTemplates, `{}`)
		var list mcp.ResourceTemplatesResult
		if err := json.Unmarshal(resp.Result, &list); err != nil {
			t.Fatalf("decode templates: %v body=%s", err, resp.raw)
		}
		count := 0
		for _, tmpl := range list.ResourceTemplates {
			if tmpl.URITemplate == mcpJobReadJobsTemplate {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("%s listed %d times, want 1; body=%s", mcpJobReadJobsTemplate, count, resp.raw)
		}
	})
	t.Run("reads a tenant-visible job", func(t *testing.T) {
		mcpJobReadRequireJob(t, mcpJobReadItem(t, srv.URL, mcpJobReadKey, "cordum://jobs/"+mcpJobReadJob))
	})
	t.Run("other-tenant job fails closed", func(t *testing.T) {
		resp := mcpJobReadRPC(t, srv.URL, mcpJobReadKey, mcp.MethodResourcesRead, `{"uri":"cordum://jobs/`+mcpJobReadOtherJob+`"}`)
		mcpJobReadRequireFailed(t, resp, "other-tenant read")
	})
	t.Run("missing job fails closed", func(t *testing.T) {
		resp := mcpJobReadRPC(t, srv.URL, mcpJobReadKey, mcp.MethodResourcesRead, `{"uri":"cordum://jobs/job-mcpjobread-missing"}`)
		mcpJobReadRequireFailed(t, resp, "missing-job read")
	})
	t.Run("percent-encoded ids fail closed", func(t *testing.T) {
		for _, uri := range []string{"cordum://jobs/a%20b", "cordum://jobs/%25zz", "cordum://jobs/a%0Ab"} {
			resp := mcpJobReadRPC(t, srv.URL, mcpJobReadKey, mcp.MethodResourcesRead, `{"uri":"`+uri+`"}`)
			mcpJobReadRequireFailed(t, resp, uri)
			mcpJobReadRequireJob(t, mcpJobReadItem(t, srv.URL, mcpJobReadKey, "cordum://jobs/"+mcpJobReadJob))
		}
	})
	t.Run("cordum_get_job reads the job", func(t *testing.T) {
		mcpJobReadRequireJob(t, mcpJobReadToolItem(t, srv.URL, mcpJobReadKey, mcp.ToolGetJob, `{"id":"`+mcpJobReadJob+`"}`))
	})
	t.Run("cordum_get_job other-tenant job fails closed", func(t *testing.T) {
		resp := mcpJobReadRPC(t, srv.URL, mcpJobReadKey, "tools/call", `{"name":"`+mcp.ToolGetJob+`","arguments":{"id":"`+mcpJobReadOtherJob+`"}}`)
		mcpJobReadRequireFailed(t, resp, "other-tenant cordum_get_job")
	})
	t.Run("cordum_get_job stays scope-gated", func(t *testing.T) {
		resp := mcpJobReadRPC(t, srv.URL, mcpJobReadNarrowKey, "tools/call", `{"name":"`+mcp.ToolGetJob+`","arguments":{"id":"`+mcpJobReadJob+`"}}`)
		if resp.Error == nil || resp.Error.Code != -32098 {
			t.Fatalf("cordum_get_job outside the agent's scope: want -32098; body=%s", resp.raw)
		}
		mcpJobReadRequireFailed(t, resp, "out-of-scope cordum_get_job")
	})
	t.Run("cordum_status answers", func(t *testing.T) {
		item := mcpJobReadToolItem(t, srv.URL, mcpJobReadKey, mcp.ToolStatus, `{}`)
		if item.Kind != "status" || item.Data["uptime_seconds"] == nil {
			t.Fatalf("cordum_status item kind=%q uptime_seconds=%v, want status with uptime", item.Kind, item.Data["uptime_seconds"])
		}
	})
}

// TestMCPJobRead_DemoSequenceReachesApproval replays the governed demo
// worker's open sequence (initialize, notifications/initialized, the job
// read and cordum_status), then its first cordum_create_workflow call,
// which must stop at exactly one pending approval.
func TestMCPJobRead_DemoSequenceReachesApproval(t *testing.T) {
	s, srv := mcpJobReadServer(t)
	approvals := s.getMCPRuntime().approvalStore

	initResp := mcpJobReadRPC(t, srv.URL, mcpJobReadKey, "initialize",
		`{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"cap-mcp-governed-worker","version":"1"}}`)
	if initResp.Error != nil {
		t.Fatalf("initialize: %s", initResp.raw)
	}
	if code, raw := mcpJobReadPost(t, srv.URL, mcpJobReadKey, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); code < 200 || code > 299 {
		t.Fatalf("notifications/initialized: HTTP %d body=%s", code, raw)
	}
	mcpJobReadRequireJob(t, mcpJobReadItem(t, srv.URL, mcpJobReadKey, "cordum://jobs/"+mcpJobReadJob))
	if item := mcpJobReadToolItem(t, srv.URL, mcpJobReadKey, mcp.ToolStatus, `{}`); item.Kind != "status" {
		t.Fatalf("cordum_status kind=%q, want status", item.Kind)
	}
	if got := pendingMCPApprovals(t, approvals, mcp.ToolCreateWorkflow); len(got) != 0 {
		t.Fatalf("pending create_workflow approvals before the call = %d, want 0", len(got))
	}
	resp := mcpJobReadRPC(t, srv.URL, mcpJobReadKey, "tools/call",
		`{"name":"`+mcp.ToolCreateWorkflow+`","arguments":{"name":"mcpjobread-demo"}}`)
	recs := pendingMCPApprovals(t, approvals, mcp.ToolCreateWorkflow)
	if len(recs) != 1 {
		t.Fatalf("pending create_workflow approvals = %d, want 1; body=%s", len(recs), resp.raw)
	}
	if rec := recs[0]; rec.AgentID != mcpJobReadAgent || rec.Principal != mcpJobReadPrincipal {
		t.Fatalf("approval AgentID=%q Principal=%q, want %s/%s", rec.AgentID, rec.Principal, mcpJobReadAgent, mcpJobReadPrincipal)
	}
	if resp.Error == nil || resp.Error.Code != -32099 {
		t.Fatalf("cordum_create_workflow: want -32099 approval required; body=%s", resp.raw)
	}
	var data struct {
		ApprovalID string `json:"approval_id"`
	}
	if err := json.Unmarshal(resp.Error.Data, &data); err != nil || data.ApprovalID != recs[0].ID {
		t.Fatalf("approval_id=%q err=%v, want stored %q; body=%s", data.ApprovalID, err, recs[0].ID, resp.raw)
	}
}
