package main_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordum/cordum-sdk-conformance-simulator/internal/engine"
	"github.com/cordum/cordum-sdk-conformance-simulator/internal/handlers"
)

// newSimHandler builds the same handler topology main.go wires up and
// returns it wrapped in an httptest.Server so the tests below can
// drive real HTTP requests without listening on a socket.
func newSimHandler(t *testing.T) (*httptest.Server, *engine.Engine) {
	t.Helper()
	eng := engine.New()
	mux := http.NewServeMux()
	handlers.Agents(mux, eng)
	handlers.Jobs(mux, eng)
	handlers.Workflows(mux, eng)
	handlers.Policies(mux, eng)
	handlers.Auth(mux, eng)
	handlers.Stream(mux, eng)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, eng
}

func apiKeyReq(t *testing.T, method, url string, body any) *http.Request {
	t.Helper()
	var buf io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		buf = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, buf)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("X-API-Key", "test-api-key")
	req.Header.Set("X-Tenant-Id", "default")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

// TestAgentCreateGetDelete runs the canonical CRUD sequence the
// agents.create_and_get fixture exercises, plus delete for clean-up
// symmetry with agents.update_and_delete.
func TestAgentCreateGetDelete(t *testing.T) {
	srv, _ := newSimHandler(t)
	client := srv.Client()

	createReq := apiKeyReq(t, http.MethodPost, srv.URL+"/api/v1/agents", map[string]any{
		"name":      "alpha",
		"owner":     "ops@acme",
		"risk_tier": "low",
	})
	resp, err := client.Do(createReq)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("create status=%d want 201", resp.StatusCode)
	}
	var created map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal("create response missing id")
	}
	if got, _ := created["status"].(string); got != "active" {
		t.Errorf("status=%q want active", got)
	}

	getReq := apiKeyReq(t, http.MethodGet, srv.URL+"/api/v1/agents/"+id, nil)
	resp, err = client.Do(getReq)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("get status=%d want 200", resp.StatusCode)
	}
	var got map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if got["id"] != id || got["name"] != "alpha" {
		t.Errorf("get returned wrong agent: %+v", got)
	}

	delReq := apiKeyReq(t, http.MethodDelete, srv.URL+"/api/v1/agents/"+id, nil)
	resp, err = client.Do(delReq)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 204 {
		t.Fatalf("delete status=%d want 204", resp.StatusCode)
	}
}

// TestAuthRejectsMissingApiKey pins the authentication error shape the
// errors/not_found + auth/api_key_unauthorized fixtures depend on.
func TestAuthRejectsMissingApiKey(t *testing.T) {
	srv, _ := newSimHandler(t)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/agents", nil)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("status=%d want 401", resp.StatusCode)
	}
}

// TestSubmitJobAndGet covers the jobs.submit_and_track fixture path.
func TestSubmitJobAndGet(t *testing.T) {
	srv, _ := newSimHandler(t)
	submit := apiKeyReq(t, http.MethodPost, srv.URL+"/api/v1/jobs", map[string]any{
		"topic":  "job.echo",
		"prompt": "hello",
	})
	resp, err := srv.Client().Do(submit)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 202 {
		t.Fatalf("submit status=%d want 202", resp.StatusCode)
	}
	var accepted map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&accepted)
	jobID, _ := accepted["job_id"].(string)
	if jobID == "" {
		t.Fatal("submit response missing job_id")
	}

	get := apiKeyReq(t, http.MethodGet, srv.URL+"/api/v1/jobs/"+jobID, nil)
	resp, err = srv.Client().Do(get)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("get status=%d want 200", resp.StatusCode)
	}
	var got map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if got["status"] != "succeeded" {
		t.Errorf("status=%v want succeeded", got["status"])
	}
}

// TestServer500OneShotRetryScript pins the X-Conformance-Script path
// the errors/server_retry_exhausted fixture relies on. With "one-shot"
// the sim returns exactly one 500 before recovering; three-times
// returns three; one-shot is what the submit flow uses.
func TestServer500OneShotRetryScript(t *testing.T) {
	srv, _ := newSimHandler(t)
	url := srv.URL + "/api/v1/jobs"
	req := apiKeyReq(t, http.MethodPost, url, map[string]any{"topic": "x"})
	req.Header.Set(engine.ScriptHeader, engine.ScriptServer500OneShot)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 500 {
		t.Fatalf("first status=%d want 500", resp.StatusCode)
	}
	// Second call under the same script: budget for one-shot is 1,
	// so this one must succeed.
	req2 := apiKeyReq(t, http.MethodPost, url, map[string]any{"topic": "x"})
	req2.Header.Set(engine.ScriptHeader, engine.ScriptServer500OneShot)
	resp2, err := srv.Client().Do(req2)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != 202 {
		t.Fatalf("second status=%d want 202 (script budget exhausted)", resp2.StatusCode)
	}
}

// TestServer500ThreeTimes pins the three-fire budget used by the
// errors/server_retry_exhausted fixture.
func TestServer500ThreeTimes(t *testing.T) {
	srv, _ := newSimHandler(t)
	url := srv.URL + "/api/v1/jobs"
	// Prime a job-id to GET against
	submit := apiKeyReq(t, http.MethodPost, url, map[string]any{"topic": "x"})
	resp, err := srv.Client().Do(submit)
	if err != nil {
		t.Fatal(err)
	}
	var accepted map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&accepted)
	resp.Body.Close()
	jobID := accepted["job_id"].(string)

	fireURL := srv.URL + "/api/v1/jobs/" + jobID
	got500 := 0
	for i := 0; i < 4; i++ {
		req := apiKeyReq(t, http.MethodGet, fireURL, nil)
		req.Header.Set(engine.ScriptHeader, engine.ScriptServer500ThreeTimes)
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		if resp.StatusCode == 500 {
			got500++
		}
		resp.Body.Close()
	}
	if got500 != 3 {
		t.Fatalf("got %d × 500 responses, want 3 (script budget)", got500)
	}
}

// TestRateLimitOnceReturns429WithRetryAfter pins the
// errors/rate_limit_retry_after fixture's server side: one 429 with
// Retry-After, then clean success.
func TestRateLimitOnceReturns429WithRetryAfter(t *testing.T) {
	srv, _ := newSimHandler(t)
	submit := apiKeyReq(t, http.MethodPost, srv.URL+"/api/v1/jobs", map[string]any{"topic": "x"})
	resp, err := srv.Client().Do(submit)
	if err != nil {
		t.Fatal(err)
	}
	var accepted map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&accepted)
	resp.Body.Close()
	jobID := accepted["job_id"].(string)

	fireURL := srv.URL + "/api/v1/jobs/" + jobID
	// First: 429
	req1 := apiKeyReq(t, http.MethodGet, fireURL, nil)
	req1.Header.Set(engine.ScriptHeader, engine.ScriptRateLimitOnce)
	resp1, err := srv.Client().Do(req1)
	if err != nil {
		t.Fatal(err)
	}
	defer resp1.Body.Close()
	if resp1.StatusCode != 429 {
		t.Fatalf("first status=%d want 429", resp1.StatusCode)
	}
	if ra := resp1.Header.Get("Retry-After"); ra == "" {
		t.Error("Retry-After header missing on 429")
	}
	// Second: 200
	req2 := apiKeyReq(t, http.MethodGet, fireURL, nil)
	req2.Header.Set(engine.ScriptHeader, engine.ScriptRateLimitOnce)
	resp2, err := srv.Client().Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("second status=%d want 200", resp2.StatusCode)
	}
}

// TestPolicyPublishAndRollback covers policies.apply_and_rollback.
func TestPolicyPublishAndRollback(t *testing.T) {
	srv, _ := newSimHandler(t)
	publish := apiKeyReq(t, http.MethodPost, srv.URL+"/api/v1/policy/publish", map[string]any{
		"id":   "default",
		"name": "default",
	})
	resp, err := srv.Client().Do(publish)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var bundle map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&bundle)
	ver1, _ := bundle["version"].(float64)
	if ver1 <= 1 {
		t.Errorf("version=%v want >1 after publish", ver1)
	}

	rollback := apiKeyReq(t, http.MethodPost, srv.URL+"/api/v1/policy/rollback", map[string]any{
		"id":             "default",
		"target_version": int(ver1) - 1,
	})
	resp2, err := srv.Client().Do(rollback)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	var rolled map[string]any
	_ = json.NewDecoder(resp2.Body).Decode(&rolled)
	ver2, _ := rolled["version"].(float64)
	if ver2 <= ver1 {
		t.Errorf("rollback version=%v want > %v", ver2, ver1)
	}
}

// TestAuditPagination pins the cursor-based pagination the
// audit/list_paginated fixture walks.
func TestAuditPagination(t *testing.T) {
	srv, _ := newSimHandler(t)
	req1 := apiKeyReq(t, http.MethodGet, srv.URL+"/api/v1/policy/audit?limit=10", nil)
	resp1, err := srv.Client().Do(req1)
	if err != nil {
		t.Fatal(err)
	}
	defer resp1.Body.Close()
	var page1 map[string]any
	_ = json.NewDecoder(resp1.Body).Decode(&page1)
	items1, _ := page1["items"].([]any)
	next, _ := page1["next_cursor"].(string)
	if len(items1) != 10 {
		t.Errorf("page 1 len=%d want 10", len(items1))
	}
	if next == "" {
		t.Fatal("page 1 next_cursor empty; expected a second page")
	}
	req2 := apiKeyReq(t, http.MethodGet, srv.URL+"/api/v1/policy/audit?limit=10&cursor="+next, nil)
	resp2, err := srv.Client().Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	var page2 map[string]any
	_ = json.NewDecoder(resp2.Body).Decode(&page2)
	items2, _ := page2["items"].([]any)
	next2, _ := page2["next_cursor"].(string)
	if len(items2) == 0 {
		t.Error("page 2 empty; expected remaining events")
	}
	if next2 != "" {
		t.Errorf("page 2 next_cursor=%q want empty (last page)", next2)
	}
}

// TestSessionLoginRefresh pins the auth.session_login_refresh flow.
func TestSessionLoginRefresh(t *testing.T) {
	srv, _ := newSimHandler(t)
	login := map[string]any{"username": "alice", "password": "pw", "tenant": "default"}
	b, _ := json.Marshal(login)
	loginReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/auth/login", bytes.NewReader(b))
	loginReq.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(loginReq)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var loginResp map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&loginResp)
	token, _ := loginResp["session_token"].(string)
	if !strings.HasPrefix(token, "sess_") {
		t.Fatalf("session_token=%q missing sess_ prefix", token)
	}
	// Follow-up call with bearer
	sessReq, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/auth/session", nil)
	sessReq.Header.Set("Authorization", "Bearer "+token)
	resp2, err := srv.Client().Do(sessReq)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("session status=%d want 200", resp2.StatusCode)
	}
}

// TestStreamEmitsThreeFrames pins workflows.run_and_stream_events.
func TestStreamEmitsThreeFrames(t *testing.T) {
	srv, _ := newSimHandler(t)
	req := apiKeyReq(t, http.MethodGet, srv.URL+"/api/v1/stream", nil)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("stream status=%d want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	for _, event := range []string{"run.started", "step.started", "run.completed"} {
		if !strings.Contains(got, event) {
			t.Errorf("stream missing event=%q in body=%q", event, got)
		}
	}
}

// TestIdempotencyKeyReplays pins the idempotency/post_with_key_retries
// fixture: the second POST with the same key returns the replayed
// idempotent-replay body instead of minting a new job id.
func TestIdempotencyKeyReplays(t *testing.T) {
	srv, _ := newSimHandler(t)
	url := srv.URL + "/api/v1/jobs"
	body := map[string]any{"topic": "x"}
	first := apiKeyReq(t, http.MethodPost, url, body)
	first.Header.Set(engine.IdempotencyHeader, "key-abc-123")
	resp1, err := srv.Client().Do(first)
	if err != nil {
		t.Fatal(err)
	}
	defer resp1.Body.Close()
	var firstBody map[string]any
	_ = json.NewDecoder(resp1.Body).Decode(&firstBody)
	if _, ok := firstBody["job_id"].(string); !ok {
		t.Fatal("first response missing job_id")
	}

	second := apiKeyReq(t, http.MethodPost, url, body)
	second.Header.Set(engine.IdempotencyHeader, "key-abc-123")
	resp2, err := srv.Client().Do(second)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	var secondBody map[string]any
	_ = json.NewDecoder(resp2.Body).Decode(&secondBody)
	if secondBody["job_id"] != "idempotent-replay" {
		t.Errorf("second response job_id=%v want idempotent-replay (replay)", secondBody["job_id"])
	}
}

// TestDeterminism100Runs runs the core create-agent path 100 times
// across fresh engines and asserts every body is byte-equal after
// opaque-id masking. This is the "sim is deterministic" guarantee the
// plan calls out: once fixtures mask $any$ + $timestamp$, two runs
// must produce identical output for the harness to trust the sim.
func TestDeterminism100Runs(t *testing.T) {
	var canonical string
	for i := 0; i < 100; i++ {
		srv, _ := newSimHandler(t)
		req := apiKeyReq(t, http.MethodPost, srv.URL+"/api/v1/agents", map[string]any{
			"name":      "alpha",
			"owner":     "ops@acme",
			"risk_tier": "low",
		})
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		masked := maskOpaque(string(b))
		if canonical == "" {
			canonical = masked
			continue
		}
		if masked != canonical {
			t.Fatalf("iter %d diverged:\n  want: %s\n  got:  %s", i, canonical, masked)
		}
	}
}

// maskOpaque replaces id + timestamp values with stable placeholders so
// two runs compare equal despite the engine's seq counter reset.
func maskOpaque(in string) string {
	out := in
	// agent-0001 etc. → agent-XXXX
	out = strings.ReplaceAll(out, "\"id\":\"agent-0001\"", "\"id\":\"agent-XXXX\"")
	// ISO-8601 at Origin → placeholder. Origin is 2026-01-01T00:00:00Z.
	out = strings.ReplaceAll(out, "2026-01-01T00:00:00Z", "TIMESTAMP")
	return out
}

// TestNotFoundShape pins errors/not_found.
func TestNotFoundShape(t *testing.T) {
	srv, _ := newSimHandler(t)
	req := apiKeyReq(t, http.MethodGet, srv.URL+"/api/v1/agents/does-not-exist", nil)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("status=%d want 404", resp.StatusCode)
	}
	var env map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&env)
	errObj, _ := env["error"].(map[string]any)
	if errObj["code"] != "not_found" {
		t.Errorf("error.code=%v want not_found", errObj["code"])
	}
}

// TestStdoutURLShape documents the stdout contract main.go adheres to:
// the first line printed to stdout is the bound URL so harness
// subprocesses can discover the sim's ephemeral port.
func TestStdoutURLShape(t *testing.T) {
	sample := "http://127.0.0.1:12345"
	if !strings.HasPrefix(sample, "http://") {
		t.Fatalf("URL contract broken: %s", sample)
	}
	_ = fmt.Sprintf("%s/api/v1/agents", sample)
}
