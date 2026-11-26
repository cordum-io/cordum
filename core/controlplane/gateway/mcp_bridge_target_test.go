package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/mcp"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// These tests pin how the gateway's in-process MCP bridge builds requests.
// A caller-supplied id or filter value is escaped into the request target
// and reaches the REST handler unchanged; a target that is not a valid
// request URI is refused with a 400 invalid_request bridge error before any
// handler runs. The HTTP cases run on the production registration sequence
// (registerMCPRoutes) with a real BasicAuthProvider, and every request is
// followed by a read of the seeded job on the same server.

const (
	mcpBridgeIDKey       = "mcpbridgeid-admin-key-0000000000000001"
	mcpBridgeIDPrincipal = "mcpbridgeid-worker"
	mcpBridgeIDAgent     = "agent-mcpbridgeid"
)

// mcpBridgeIDValues are the caller values each id-taking surface gets:
// enc goes into a resource URI, raw is what a tool argument carries.
var mcpBridgeIDValues = []struct{ name, enc, raw string }{
	{"encoded space", "a%20b", "a b"},
	{"encoded percent", "%25zz", "%zz"},
	{"trailing space", "x%20", "x "},
}

func TestMCPBridgeInvoke_RefusesInvalidTarget(t *testing.T) {
	t.Parallel()
	s := &server{tenant: "default"}
	calls := 0
	handler := func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"` + r.PathValue("id") + `","path":"` + r.URL.Path + `"}`))
	}
	invokers := map[string]func(target string, pathValues map[string]string) (int, []byte, error){
		"json": func(target string, pv map[string]string) (int, []byte, error) {
			status, _, raw, err := s.invokeMCPJSONHandler(context.Background(), http.MethodGet, target, nil, pv, nil, handler)
			return status, raw, err
		},
		"any": func(target string, pv map[string]string) (int, []byte, error) {
			status, _, raw, err := s.invokeMCPAnyHandler(context.Background(), http.MethodGet, target, nil, pv, nil, handler)
			return status, raw, err
		},
	}
	for name, invoke := range invokers {
		for _, target := range []string{"", "api/v1/jobs", "/api/v1/jobs/a b", "/api/v1/jobs/%zz", "/api/v1/jobs/a\nb", "/api/v1/jobs/a\x7fb"} {
			calls = 0
			status, raw, err := invoke(target, map[string]string{"id": "x"})
			var be *mcp.BridgeError
			if !errors.As(err, &be) || be.StatusCode != http.StatusBadRequest || be.Code != "invalid_request" {
				t.Fatalf("%s %q: status=%d err=%v, want a 400 invalid_request BridgeError", name, target, status, err)
			}
			if calls != 0 || status != 0 || raw != nil {
				t.Fatalf("%s %q: handler calls=%d status=%d body=%s, want no handler run", name, target, calls, status, raw)
			}
		}
		calls = 0
		status, raw, err := invoke("/api/v1/jobs/"+url.PathEscape("a b%zz/c"), map[string]string{"id": "a b%zz/c"})
		if err != nil || status != http.StatusOK || calls != 1 {
			t.Fatalf("%s escaped target: status=%d calls=%d err=%v, want one handler run", name, status, calls, err)
		}
		if !strings.Contains(string(raw), `"id":"a b%zz/c"`) || !strings.Contains(string(raw), `"path":"/api/v1/jobs/a b%zz/c"`) {
			t.Fatalf("%s escaped target: body=%s, want the raw id in the path value and the decoded path", name, raw)
		}
	}
}

// mcpBridgeIDServer serves the HTTP MCP routes for one static admin key in
// tenant "default", whose agent may call every cordum_* tool, and seeds the
// job that mcpJobReadRequireJob checks.
func mcpBridgeIDServer(t *testing.T) *httptest.Server {
	t.Helper()
	s, _, _ := newTestGateway(t)
	setMCPConfigForBoot(t, s, true, false)
	ctx := context.Background()
	if _, err := s.agentIdentityStore.Create(ctx, store.AgentIdentity{
		ID: mcpBridgeIDAgent, TenantID: "default", Name: mcpBridgeIDAgent, Owner: "alice@example.com",
		RiskTier: "critical", AllowedTools: []string{"cordum_*"}, Status: "active",
		DataClassifications: []string{"pii", "phi", "secrets"},
	}); err != nil {
		t.Fatalf("create agent identity: %v", err)
	}
	if err := s.agentIdentityStore.LinkWorker(ctx, mcpBridgeIDAgent, mcpBridgeIDPrincipal); err != nil {
		t.Fatalf("LinkWorker: %v", err)
	}
	keys, err := json.Marshal([]map[string]string{{"key": mcpBridgeIDKey, "tenant": "default", "role": "admin", "principal_id": mcpBridgeIDPrincipal}})
	if err != nil {
		t.Fatalf("marshal api keys: %v", err)
	}
	s.auth = newBasicAuthForTest(t, map[string]string{"CORDUM_API_KEYS": string(keys)})
	if err := s.jobStore.SetJobMeta(ctx, &pb.JobRequest{JobId: mcpJobReadJob, Topic: "job.mcpbridgeid.visible", TenantId: "default", PrincipalId: mcpBridgeIDPrincipal}); err != nil {
		t.Fatalf("SetJobMeta: %v", err)
	}
	if err := s.jobStore.SetState(ctx, mcpJobReadJob, model.JobStatePending); err != nil {
		t.Fatalf("SetState: %v", err)
	}
	s.shutdownCh = make(chan struct{})
	t.Cleanup(func() { close(s.shutdownCh) })
	mux := http.NewServeMux()
	if err := s.registerMCPRoutes(mux); err != nil {
		t.Fatalf("register mcp routes: %v", err)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// mcpBridgeIDThenServed sends one request, then requires a read on the same
// server to return the seeded job.
func mcpBridgeIDThenServed(t *testing.T, base, method, params string) mcpJobReadResponse {
	t.Helper()
	resp := mcpJobReadRPC(t, base, mcpBridgeIDKey, method, params)
	mcpJobReadRequireJob(t, mcpJobReadItem(t, base, mcpBridgeIDKey, "cordum://jobs/"+mcpJobReadJob))
	return resp
}

func TestMCPBridgeIDs_HTTPRouteAnswersEveryValue(t *testing.T) {
	srv := mcpBridgeIDServer(t)

	t.Run("resources reach the read handlers", func(t *testing.T) {
		for _, v := range mcpBridgeIDValues {
			for _, uri := range []string{"cordum://workflows/" + v.enc + "/runs", "cordum://workflows/wf-mcpbridgeid/runs/" + v.enc} {
				resp := mcpBridgeIDThenServed(t, srv.URL, mcp.MethodResourcesRead, `{"uri":"`+uri+`"}`)
				want := -32601 // the read handler's not-found answer
				if v.name == "encoded percent" {
					want = -32602 // refused while the resource URI is parsed
				}
				if resp.Error == nil || resp.Error.Code != want {
					t.Fatalf("%s: want error %d; body=%s", uri, want, resp.raw)
				}
			}
			uri := "cordum://jobs?status=" + v.enc
			resp := mcpBridgeIDThenServed(t, srv.URL, mcp.MethodResourcesRead, `{"uri":"`+uri+`"}`)
			var read mcp.ResourceReadResult
			if resp.Error != nil || json.Unmarshal(resp.Result, &read) != nil || len(read.Contents) != 1 {
				t.Fatalf("%s: want one content item; body=%s", uri, resp.raw)
			}
			var page struct {
				Items []map[string]any `json:"items"`
			}
			if err := json.Unmarshal([]byte(read.Contents[0].Text), &page); err != nil || page.Items == nil || len(page.Items) != 0 {
				t.Fatalf("%s: items=%v err=%v, want an empty list for a state no job has; body=%s", uri, page.Items, err, resp.raw)
			}
		}
	})

	t.Run("id-taking tools reach their handlers", func(t *testing.T) {
		tools := []struct{ name, field, extra, code string }{
			{mcp.ToolCancelJob, "job_id", "", "job_not_found"},
			{mcp.ToolTriggerWorkflow, "workflow_id", "", "workflow_not_found"},
			{mcp.ToolApproveJob, "job_id", "", "job_not_found"},
			{mcp.ToolRejectJob, "job_id", `,"reason":"r"`, "job_not_found"},
		}
		for _, v := range mcpBridgeIDValues {
			raw, _ := json.Marshal(v.raw)
			for _, tool := range tools {
				resp := mcpBridgeIDThenServed(t, srv.URL, "tools/call", `{"name":"`+tool.name+`","arguments":{"`+tool.field+`":`+string(raw)+tool.extra+`}}`)
				var result struct {
					IsError    bool `json:"isError"`
					Structured struct {
						Error struct {
							Code   string `json:"code"`
							Status int    `json:"status"`
						} `json:"error"`
					} `json:"structuredContent"`
				}
				if resp.Error != nil || json.Unmarshal(resp.Result, &result) != nil || !result.IsError ||
					result.Structured.Error.Code != tool.code || result.Structured.Error.Status != http.StatusNotFound {
					t.Fatalf("%s %s=%q: want a %s tool error; body=%s", tool.name, tool.field, v.raw, tool.code, resp.raw)
				}
			}
			resp := mcpBridgeIDThenServed(t, srv.URL, "tools/call", `{"name":"`+mcp.ToolGetJob+`","arguments":{"id":`+string(raw)+`}}`)
			mcpJobReadRequireFailed(t, resp, mcp.ToolGetJob+" "+v.name)
		}
	})

	t.Run("every resource template answers", func(t *testing.T) {
		list := mcpJobReadRPC(t, srv.URL, mcpBridgeIDKey, mcp.MethodResourceTemplates, `{}`)
		var templates mcp.ResourceTemplatesResult
		if err := json.Unmarshal(list.Result, &templates); err != nil || len(templates.ResourceTemplates) < 10 {
			t.Fatalf("templates=%d err=%v, want at least 10; body=%s", len(templates.ResourceTemplates), err, list.raw)
		}
		for _, tmpl := range templates.ResourceTemplates {
			for _, v := range mcpBridgeIDValues {
				uri := tmpl.URITemplate
				for strings.Contains(uri, "{") {
					open := strings.Index(uri, "{")
					end := strings.Index(uri[open:], "}")
					if end < 0 {
						t.Fatalf("template %q has an unclosed placeholder", tmpl.URITemplate)
					}
					uri = uri[:open] + v.enc + uri[open+end+1:]
				}
				resp := mcpBridgeIDThenServed(t, srv.URL, mcp.MethodResourcesRead, `{"uri":"`+uri+`"}`)
				if resp.Error == nil {
					t.Fatalf("%s (%s): want an error for an id no record has; body=%s", tmpl.URITemplate, v.name, resp.raw)
				}
			}
		}
	})
}
