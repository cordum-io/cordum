package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// BUG-1: Handlers that previously bypassed body size limits by using raw
// json.NewDecoder instead of decodeJSONBody. These tests prove the fix works.
// ---------------------------------------------------------------------------

func TestCreateWorkflow_OversizedBody(t *testing.T) {
	s, _, _ := newTestGateway(t)
	// Build a valid-JSON payload that exceeds 2MB default limit.
	bigName := strings.Repeat("a", int(defaultMaxJSONBodyBytes)+1)
	body := fmt.Sprintf(`{"name":"%s"}`, bigName)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", "default")
	rr := httptest.NewRecorder()
	s.handleCreateWorkflow(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code, "expected 403 for oversized workflow body")
	assert.Contains(t, rr.Body.String(), "tier_limit_exceeded")
}

func TestCreateWorkflow_MalformedJSON(t *testing.T) {
	s, _, _ := newTestGateway(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows", strings.NewReader(`{invalid json`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", "default")
	rr := httptest.NewRecorder()
	s.handleCreateWorkflow(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code, "expected 400 for malformed json")
}

func TestCreateWorkflow_EmptyBody(t *testing.T) {
	s, _, _ := newTestGateway(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", "default")
	rr := httptest.NewRecorder()
	s.handleCreateWorkflow(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code, "expected 400 for empty body")
}

func TestCreateWorkflow_NameTooLong(t *testing.T) {
	s, _, _ := newTestGateway(t)
	longName := strings.Repeat("x", 300)
	body := fmt.Sprintf(`{"name":"%s","steps":{}}`, longName)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", "default")
	rr := httptest.NewRecorder()
	s.handleCreateWorkflow(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code, "expected 400 for name too long")
	assert.Contains(t, rr.Body.String(), "name too long")
}

func TestCreateWorkflow_NegativeTimeout(t *testing.T) {
	s, _, _ := newTestGateway(t)
	body := `{"name":"test","timeout_sec":-1,"steps":{}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", "default")
	rr := httptest.NewRecorder()
	s.handleCreateWorkflow(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code, "expected 400 for negative timeout")
	assert.Contains(t, rr.Body.String(), "non-negative")
}

func TestCreateWorkflow_ExcessiveTimeout(t *testing.T) {
	s, _, _ := newTestGateway(t)
	body := `{"name":"test","timeout_sec":999999999,"steps":{}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", "default")
	rr := httptest.NewRecorder()
	s.handleCreateWorkflow(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code, "expected 400 for excessive timeout")
	assert.Contains(t, rr.Body.String(), "too large")
}

// ---------------------------------------------------------------------------
// BUG-2: Unclamped list limits — handlers that accepted unbounded limit params
// ---------------------------------------------------------------------------

func TestListAllRuns_LimitClamped(t *testing.T) {
	s, _, _ := newTestGateway(t)
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/workflow-runs?limit=%d", maxListLimit+1000), nil)
	req.Header.Set("X-Tenant-ID", "default")
	rr := httptest.NewRecorder()
	s.handleListAllRuns(rr, req)

	// The request should succeed but with clamped limit (no crash, no OOM).
	assert.Equal(t, http.StatusOK, rr.Code)
	var resp map[string]any
	assert.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
}

func TestGetRunTimeline_LimitClamped(t *testing.T) {
	s, _, _ := newTestGateway(t)
	// Create a minimal run first.
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/workflow-runs/nonexistent/timeline?limit=%d", maxListLimit+1000), nil)
	req.SetPathValue("id", "nonexistent")
	req.Header.Set("X-Tenant-ID", "default")
	rr := httptest.NewRecorder()
	s.handleGetRunTimeline(rr, req)

	// Should succeed (empty result) or 200, not crash.
	assert.Contains(t, []int{http.StatusOK, http.StatusNotFound}, rr.Code)
}

// ---------------------------------------------------------------------------
// BUG-5: Lock handlers missing validation
// ---------------------------------------------------------------------------

func TestAcquireLock_MissingResource(t *testing.T) {
	s, _, _ := newTestGateway(t)
	body := `{"resource":"","owner":"test-owner","ttl_ms":5000}`
	req := adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/locks/acquire", strings.NewReader(body)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.handleAcquireLock(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Contains(t, rr.Body.String(), "resource required")
}

func TestAcquireLock_MissingOwner(t *testing.T) {
	s, _, _ := newTestGateway(t)
	body := `{"resource":"my-resource","owner":"","ttl_ms":5000}`
	req := adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/locks/acquire", strings.NewReader(body)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.handleAcquireLock(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Contains(t, rr.Body.String(), "owner required")
}

func TestAcquireLock_ResourceTooLong(t *testing.T) {
	s, _, _ := newTestGateway(t)
	longResource := strings.Repeat("x", 600)
	body := fmt.Sprintf(`{"resource":"%s","owner":"test","ttl_ms":5000}`, longResource)
	req := adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/locks/acquire", strings.NewReader(body)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.handleAcquireLock(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Contains(t, rr.Body.String(), "resource too long")
}

func TestAcquireLock_OwnerTooLong(t *testing.T) {
	s, _, _ := newTestGateway(t)
	longOwner := strings.Repeat("y", 300)
	body := fmt.Sprintf(`{"resource":"r","owner":"%s","ttl_ms":5000}`, longOwner)
	req := adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/locks/acquire", strings.NewReader(body)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.handleAcquireLock(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Contains(t, rr.Body.String(), "owner too long")
}

func TestAcquireLock_NegativeTTL(t *testing.T) {
	s, _, _ := newTestGateway(t)
	body := `{"resource":"r","owner":"o","ttl_ms":-1}`
	req := adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/locks/acquire", strings.NewReader(body)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.handleAcquireLock(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Contains(t, rr.Body.String(), "non-negative")
}

func TestAcquireLock_ExcessiveTTL(t *testing.T) {
	s, _, _ := newTestGateway(t)
	body := `{"resource":"r","owner":"o","ttl_ms":9999999}`
	req := adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/locks/acquire", strings.NewReader(body)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.handleAcquireLock(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Contains(t, rr.Body.String(), "too large")
}

func TestAcquireLock_OversizedBody(t *testing.T) {
	s, _, _ := newTestGateway(t)
	bigResource := strings.Repeat("a", int(defaultMaxJSONBodyBytes)+1)
	body := fmt.Sprintf(`{"resource":"%s","owner":"o","ttl_ms":1000}`, bigResource)
	req := adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/locks/acquire", strings.NewReader(body)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.handleAcquireLock(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
	assert.Contains(t, rr.Body.String(), "tier_limit_exceeded")
}

func TestReleaseLock_MalformedJSON(t *testing.T) {
	s, _, _ := newTestGateway(t)
	req := adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/locks/release", strings.NewReader("{bad")))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.handleReleaseLock(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestRenewLock_MalformedJSON(t *testing.T) {
	s, _, _ := newTestGateway(t)
	req := adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/locks/renew", strings.NewReader("{bad")))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.handleRenewLock(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// ---------------------------------------------------------------------------
// Fuzz-like malformed payload tests across multiple handlers
// ---------------------------------------------------------------------------

func TestStartRun_OversizedBody(t *testing.T) {
	s, _, _ := newTestGateway(t)
	// Create a workflow first so StartRun can find it.
	wfBody := `{"name":"test","steps":{}}`
	wfReq := httptest.NewRequest(http.MethodPost, "/api/v1/workflows", strings.NewReader(wfBody))
	wfReq.Header.Set("Content-Type", "application/json")
	wfReq.Header.Set("X-Tenant-ID", "default")
	wfRR := httptest.NewRecorder()
	s.handleCreateWorkflow(wfRR, wfReq)
	assert.Equal(t, http.StatusCreated, wfRR.Code)
	var wfResp map[string]string
	_ = json.NewDecoder(wfRR.Body).Decode(&wfResp)
	wfID := wfResp["id"]

	bigInput := strings.Repeat("x", int(defaultMaxJSONBodyBytes)+1)
	body := fmt.Sprintf(`{"input":"%s"}`, bigInput)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows/"+wfID+"/runs", strings.NewReader(body))
	req.SetPathValue("id", wfID)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", "default")
	rr := httptest.NewRecorder()
	s.handleStartRun(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
	assert.Contains(t, rr.Body.String(), "tier_limit_exceeded")
}

func TestStartRun_MalformedJSON(t *testing.T) {
	s, _, _ := newTestGateway(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows/wf1/runs", strings.NewReader("{malformed"))
	req.SetPathValue("id", "wf1")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", "default")
	rr := httptest.NewRecorder()
	s.handleStartRun(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestRerunRun_MalformedJSON(t *testing.T) {
	s, _, _ := newTestGateway(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflow-runs/run1/rerun", strings.NewReader("{malformed"))
	req.SetPathValue("id", "run1")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", "default")
	rr := httptest.NewRecorder()
	s.handleRerunRun(rr, req)

	// The handler checks workflowEng availability first (503), then body.
	// In test env without workflow engine, we get 503. Either way, no panic.
	assert.Contains(t, []int{http.StatusBadRequest, http.StatusServiceUnavailable}, rr.Code,
		"expected 400 or 503, not 500/panic")
}

func TestPolicyCheck_OversizedBody(t *testing.T) {
	s, _, _ := newTestGateway(t)
	bigTopic := strings.Repeat("a", int(defaultMaxJSONBodyBytes)+1)
	body := fmt.Sprintf(`{"topic":"%s"}`, bigTopic)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/policy/evaluate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", "default")
	rr := httptest.NewRecorder()
	s.handlePolicyCheck(rr, req, "evaluate")

	assert.Equal(t, http.StatusForbidden, rr.Code)
	assert.Contains(t, rr.Body.String(), "tier_limit_exceeded")
}

// ---------------------------------------------------------------------------
// Validate submit job edge cases
// ---------------------------------------------------------------------------

func TestSubmitJobValidation_PromptTooLong(t *testing.T) {
	longPrompt := strings.Repeat("x", maxPromptChars+1)
	req := submitJobRequest{
		Prompt: longPrompt,
		Topic:  "job.default",
	}
	req.applyDefaults("tenant")
	err := req.validate("tenant")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "prompt too long")
}

func TestSubmitJobValidation_TopicTooLong(t *testing.T) {
	req := submitJobRequest{
		Prompt: "test",
		Topic:  "job." + strings.Repeat("a", 260),
	}
	req.applyDefaults("tenant")
	err := req.validate("tenant")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "topic too long")
}

func TestSubmitJobValidation_TooManyTags(t *testing.T) {
	tags := make([]string, 51)
	for i := range tags {
		tags[i] = fmt.Sprintf("tag-%d", i)
	}
	req := submitJobRequest{
		Prompt: "test",
		Topic:  "job.default",
		Tags:   tags,
	}
	req.applyDefaults("tenant")
	err := req.validate("tenant")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "too many tags")
}

func TestSubmitJobValidation_TooManyLabels(t *testing.T) {
	labels := make(map[string]string, 51)
	for i := 0; i < 51; i++ {
		labels[fmt.Sprintf("key-%d", i)] = "value"
	}
	req := submitJobRequest{
		Prompt: "test",
		Topic:  "job.default",
		Labels: labels,
	}
	req.applyDefaults("tenant")
	err := req.validate("tenant")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "too many labels")
}

func TestSubmitJobValidation_NegativeDeadline(t *testing.T) {
	req := submitJobRequest{
		Prompt:     "test",
		Topic:      "job.default",
		DeadlineMs: -100,
	}
	req.applyDefaults("tenant")
	err := req.validate("tenant")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "deadline_ms must be non-negative")
}

func TestSubmitJobValidation_NilRequest(t *testing.T) {
	var req *submitJobRequest
	err := req.validate("tenant")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "request required")
}

// ---------------------------------------------------------------------------
// Validate decodeJSONBody edge cases
// ---------------------------------------------------------------------------

func TestDecodeJSONBody_NilRequest(t *testing.T) {
	var dst map[string]any
	err := decodeJSONBody(httptest.NewRecorder(), nil, &dst)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "request required")
}

func TestDecodeJSONBody_TruncatedJSON(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"key":"val`))
	var dst map[string]any
	err := decodeJSONBody(w, r, &dst)
	assert.Error(t, err)
}

func TestDecodeJSONBody_InvalidUTF8(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{\"key\":\"\xff\xff\"}"))
	var dst map[string]any
	err := decodeJSONBody(w, r, &dst)
	// Should not panic — either succeeds (Go allows some invalid UTF-8 in JSON) or returns error.
	_ = err
}

func TestDecodeJSONBody_ArrayInsteadOfObject(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`[1,2,3]`))
	var dst map[string]any
	err := decodeJSONBody(w, r, &dst)
	// json.Decoder returns type mismatch error, not panic.
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// validateLockRequest unit tests
// ---------------------------------------------------------------------------

func TestValidateLockRequest(t *testing.T) {
	tests := []struct {
		name    string
		req     lockRequest
		wantErr string
	}{
		{
			name:    "empty resource",
			req:     lockRequest{Resource: "", Owner: "o", TTLms: 1000},
			wantErr: "resource required",
		},
		{
			name:    "whitespace only resource",
			req:     lockRequest{Resource: "   ", Owner: "o", TTLms: 1000},
			wantErr: "resource required",
		},
		{
			name:    "empty owner",
			req:     lockRequest{Resource: "r", Owner: "", TTLms: 1000},
			wantErr: "owner required",
		},
		{
			name:    "resource too long",
			req:     lockRequest{Resource: strings.Repeat("x", 513), Owner: "o", TTLms: 1000},
			wantErr: "resource too long",
		},
		{
			name:    "owner too long",
			req:     lockRequest{Resource: "r", Owner: strings.Repeat("x", 257), TTLms: 1000},
			wantErr: "owner too long",
		},
		{
			name:    "negative ttl",
			req:     lockRequest{Resource: "r", Owner: "o", TTLms: -1},
			wantErr: "non-negative",
		},
		{
			name:    "excessive ttl",
			req:     lockRequest{Resource: "r", Owner: "o", TTLms: 7200000},
			wantErr: "too large",
		},
		{
			name:    "valid request",
			req:     lockRequest{Resource: "my-resource", Owner: "my-owner", TTLms: 30000},
			wantErr: "",
		},
		{
			name:    "zero ttl is valid",
			req:     lockRequest{Resource: "r", Owner: "o", TTLms: 0},
			wantErr: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateLockRequest(tt.req)
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Workflow step ID validation edge cases
// ---------------------------------------------------------------------------

func TestWorkflowStepIDValidation(t *testing.T) {
	tests := []struct {
		name    string
		stepID  string
		wantErr bool
	}{
		{"valid", "step1", false},
		{"valid-with-dots", "step.1", false},
		{"valid-with-hyphens", "step-1", false},
		{"valid-with-underscores", "step_1", false},
		{"empty", "", true},
		{"too-long", strings.Repeat("x", 65), true},
		{"starts-with-dot", ".step", true},
		{"starts-with-hyphen", "-step", true},
		{"special-chars", "step;inject", true},
		{"spaces", "step 1", true},
		{"newlines", "step\n1", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateWorkflowStepID(tt.stepID)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// clampListLimit edge cases
// ---------------------------------------------------------------------------

func TestClampListLimit(t *testing.T) {
	assert.Equal(t, int64(0), clampListLimit(0), "zero passes through")
	assert.Equal(t, int64(-1), clampListLimit(-1), "negative passes through")
	assert.Equal(t, int64(50), clampListLimit(50), "within range passes through")
	assert.Equal(t, maxListLimit, clampListLimit(maxListLimit), "exact max passes through")
	assert.Equal(t, maxListLimit, clampListLimit(maxListLimit+1), "over max clamped")
	assert.Equal(t, maxListLimit, clampListLimit(999999), "large value clamped")
}

// ===========================================================================
// STEP 4: Panic-Resilience & Error-Envelope Contract Tests
// ===========================================================================

// panicSafeCall invokes a handler function and recovers from panics, failing the test.
func panicSafeCall(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("PANIC in %s: %v", name, r)
		}
	}()
	fn()
}

// assertBoundedErrorEnvelope checks that an error response:
// 1. Has Content-Type application/json
// 2. Contains {"error": string, "status": int} with bounded values
// 3. Never leaks internal details (file paths, Redis URLs, stack traces)
func assertBoundedErrorEnvelope(t *testing.T, rr *httptest.ResponseRecorder, testName string) {
	t.Helper()
	if rr.Code < 400 {
		return // success responses don't need error envelope checks
	}
	body := rr.Body.String()
	if body == "" {
		return // some handlers write status-only (e.g. 204)
	}
	ct := rr.Header().Get("Content-Type")
	assert.Contains(t, ct, "application/json", "%s: error response should be JSON", testName)

	// Body should be bounded — never more than 4KB for an error response
	assert.Less(t, len(body), 4096, "%s: error body exceeds 4KB safety limit", testName)

	// Should not leak internal details
	internalLeaks := []string{
		"redis://", "nats://", "localhost:",
		".go:", "goroutine ", "runtime.",
		"panic:", "PANIC",
	}
	for _, leak := range internalLeaks {
		assert.NotContains(t, body, leak, "%s: error body leaks internal detail: %s", testName, leak)
	}

	// Should be valid JSON with "error" field
	var envelope map[string]any
	if err := json.Unmarshal([]byte(body), &envelope); err == nil {
		if errMsg, ok := envelope["error"].(string); ok {
			// Error message should be bounded (2KB max — allows field names in validation errors).
			// NOTE: BUG-7 identified: some error messages echo user-supplied field names
			// (e.g., 1000-char step IDs). Production fix should truncate echoed input.
			assert.Less(t, len(errMsg), 2048, "%s: error message too long", testName)
		}
	}
}

// ---------------------------------------------------------------------------
// Panic-resilience: Send hostile payloads to every handler category
// ---------------------------------------------------------------------------

func TestPanicResilience_WorkflowHandlers(t *testing.T) {
	hostilePayloads := []struct {
		name string
		body string
	}{
		{"null_body", "null"},
		{"number_body", "42"},
		{"bool_body", "true"},
		{"nested_deep", `{"a":{"b":{"c":{"d":{"e":{"f":{"g":{"h":{"i":{"j":` + strings.Repeat(`{"k":`, 50) + `1` + strings.Repeat(`}`, 50) + `}}}}}}}}}}`},
		{"huge_array_keys", `{"steps":{"` + strings.Repeat("k", 1000) + `":{}}}`},
		{"unicode_bomb", `{"name":"` + strings.Repeat("\u0000", 100) + `"}`},
		{"negative_numbers", `{"timeout_sec":-999999999999}`},
		{"float_as_int", `{"timeout_sec":1.5}`},
		{"string_as_number", `{"timeout_sec":"not_a_number"}`},
		{"empty_object", `{}`},
		{"extra_fields", `{"name":"test","unknown_field_1":"val","unknown_field_2":null,"steps":{}}`},
	}
	for _, tt := range hostilePayloads {
		t.Run("CreateWorkflow/"+tt.name, func(t *testing.T) {
			s, _, _ := newTestGateway(t)
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Tenant-ID", "default")
			panicSafeCall(t, "handleCreateWorkflow/"+tt.name, func() {
				s.handleCreateWorkflow(rr, req)
			})
			assertBoundedErrorEnvelope(t, rr, "CreateWorkflow/"+tt.name)
		})
	}
}

func TestPanicResilience_LockHandlers(t *testing.T) {
	hostilePayloads := []struct {
		name string
		body string
	}{
		{"null", "null"},
		{"empty", "{}"},
		{"zero_values", `{"resource":"","owner":"","ttl_ms":0}`},
		{"max_int", `{"resource":"r","owner":"o","ttl_ms":9223372036854775807}`},
		{"negative_all", `{"resource":"r","owner":"o","ttl_ms":-9223372036854775808}`},
		{"string_ttl", `{"resource":"r","owner":"o","ttl_ms":"not_number"}`},
		{"array_resource", `{"resource":["a","b"],"owner":"o","ttl_ms":1000}`},
		{"null_fields", `{"resource":null,"owner":null,"ttl_ms":null}`},
	}
	handlers := []struct {
		name    string
		path    string
		handler string
	}{
		{"Acquire", "/api/v1/locks/acquire", "acquire"},
		{"Release", "/api/v1/locks/release", "release"},
		{"Renew", "/api/v1/locks/renew", "renew"},
	}
	for _, h := range handlers {
		for _, tt := range hostilePayloads {
			t.Run(h.name+"/"+tt.name, func(t *testing.T) {
				s, _, _ := newTestGateway(t)
				rr := httptest.NewRecorder()
				req := adminCtx(httptest.NewRequest(http.MethodPost, h.path, strings.NewReader(tt.body)))
				req.Header.Set("Content-Type", "application/json")
				panicSafeCall(t, h.name+"/"+tt.name, func() {
					switch h.handler {
					case "acquire":
						s.handleAcquireLock(rr, req)
					case "release":
						s.handleReleaseLock(rr, req)
					case "renew":
						s.handleRenewLock(rr, req)
					}
				})
				assertBoundedErrorEnvelope(t, rr, h.name+"/"+tt.name)
			})
		}
	}
}

func TestPanicResilience_JobHandlers(t *testing.T) {
	hostilePayloads := []struct {
		name string
		body string
	}{
		{"null", "null"},
		{"empty_object", "{}"},
		{"missing_prompt", `{"topic":"job.default"}`},
		{"empty_prompt", `{"prompt":"","topic":"job.default"}`},
		{"null_labels", `{"prompt":"test","topic":"job.default","labels":null}`},
		{"labels_as_array", `{"prompt":"test","topic":"job.default","labels":["a"]}`},
		{"nested_context", `{"prompt":"test","topic":"job.default","context":{"a":{"b":{"c":"d"}}}}`},
		{"huge_tags", `{"prompt":"test","topic":"job.default","tags":[` + strings.Repeat(`"tag",`, 200) + `"last"]}`},
		{"float_deadline", `{"prompt":"test","topic":"job.default","deadline_ms":1.5}`},
		{"string_deadline", `{"prompt":"test","topic":"job.default","deadline_ms":"never"}`},
	}
	for _, tt := range hostilePayloads {
		t.Run("SubmitJob/"+tt.name, func(t *testing.T) {
			s, _, _ := newTestGateway(t)
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Tenant-ID", "default")
			panicSafeCall(t, "SubmitJob/"+tt.name, func() {
				s.handleSubmitJobHTTP(rr, req)
			})
			assertBoundedErrorEnvelope(t, rr, "SubmitJob/"+tt.name)
		})
	}
}

func TestPanicResilience_RemediateJob(t *testing.T) {
	hostilePayloads := []struct {
		name string
		body string
	}{
		{"null", "null"},
		{"empty", "{}"},
		{"invalid_action", `{"action":"evil_action"}`},
		{"missing_action", `{"reason":"test"}`},
		{"array_body", `["action","release"]`},
	}
	for _, tt := range hostilePayloads {
		t.Run(tt.name, func(t *testing.T) {
			s, _, _ := newTestGateway(t)
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/fake-id/remediate", strings.NewReader(tt.body))
			req.SetPathValue("id", "fake-id")
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Tenant-ID", "default")
			panicSafeCall(t, "RemediateJob/"+tt.name, func() {
				s.handleRemediateJob(rr, req)
			})
			assertBoundedErrorEnvelope(t, rr, "RemediateJob/"+tt.name)
		})
	}
}

func TestPanicResilience_PolicyBundleHandlers(t *testing.T) {
	hostilePayloads := []struct {
		name string
		body string
	}{
		{"null", "null"},
		{"empty_object", "{}"},
		{"array", "[1,2,3]"},
		{"string", `"just a string"`},
		{"nested_deep", `{"rules":{"a":{"b":{"c":"d"}}}}`},
	}
	for _, tt := range hostilePayloads {
		t.Run("PutBundle/"+tt.name, func(t *testing.T) {
			s, _, _ := newTestGateway(t)
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPut, "/api/v1/policy/bundles/test", strings.NewReader(tt.body))
			req.SetPathValue("scope", "test")
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Tenant-ID", "default")
			panicSafeCall(t, "PutBundle/"+tt.name, func() {
				s.handlePutPolicyBundle(rr, req)
			})
			assertBoundedErrorEnvelope(t, rr, "PutBundle/"+tt.name)
		})
	}
}

func TestPanicResilience_AuthHandlers(t *testing.T) {
	hostilePayloads := []struct {
		name string
		body string
	}{
		{"null", "null"},
		{"empty", "{}"},
		{"missing_user", `{"password":"test"}`},
		{"missing_pass", `{"username":"test"}`},
		{"array", `["user","pass"]`},
		{"number", `42`},
		{"unicode_user", `{"username":"` + strings.Repeat("\u200b", 100) + `","password":"p"}`},
	}
	for _, tt := range hostilePayloads {
		t.Run("Login/"+tt.name, func(t *testing.T) {
			s, _, _ := newTestGateway(t)
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			panicSafeCall(t, "Login/"+tt.name, func() {
				s.handleLogin(rr, req)
			})
			assertBoundedErrorEnvelope(t, rr, "Login/"+tt.name)
		})
	}
}

// ---------------------------------------------------------------------------
// Error envelope contract: Verify all error codes have valid JSON envelopes
// ---------------------------------------------------------------------------

func TestErrorEnvelopeContract_WriteErrorJSON(t *testing.T) {
	statusCodes := []int{
		http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusNotFound,
		http.StatusConflict,
		http.StatusRequestEntityTooLarge,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusServiceUnavailable,
		http.StatusBadGateway,
	}
	for _, code := range statusCodes {
		t.Run(fmt.Sprintf("status_%d", code), func(t *testing.T) {
			rr := httptest.NewRecorder()
			writeErrorJSON(rr, code, "test error message")
			assert.Equal(t, code, rr.Code)
			assert.Contains(t, rr.Header().Get("Content-Type"), "application/json")
			var envelope map[string]any
			err := json.NewDecoder(rr.Body).Decode(&envelope)
			assert.NoError(t, err, "error response must be valid JSON")
			assert.Equal(t, "test error message", envelope["error"])
			assert.Equal(t, float64(code), envelope["status"])
		})
	}
}

func TestErrorEnvelopeContract_WriteErrorJSON_SpecialChars(t *testing.T) {
	specialMessages := []string{
		"",
		"<script>alert(1)</script>",
		"path: C:\\Users\\data\\file.go",
		strings.Repeat("a", 1000),
		"null",
		`"quoted"`,
		"line1\nline2\ttab",
		"\x00\x01\x02",
	}
	for i, msg := range specialMessages {
		t.Run(fmt.Sprintf("special_%d", i), func(t *testing.T) {
			rr := httptest.NewRecorder()
			panicSafeCall(t, fmt.Sprintf("writeErrorJSON/special_%d", i), func() {
				writeErrorJSON(rr, http.StatusBadRequest, msg)
			})
			// Must always produce valid JSON, never panic
			var envelope map[string]any
			err := json.NewDecoder(rr.Body).Decode(&envelope)
			assert.NoError(t, err, "error response must be valid JSON even with special chars")
		})
	}
}

// ---------------------------------------------------------------------------
// Nil-body and nil-request resilience
// ---------------------------------------------------------------------------

func TestPanicResilience_NilBodyReaders(t *testing.T) {
	handlers := []struct {
		name string
		call func(s *server, rr *httptest.ResponseRecorder)
	}{
		{"CreateWorkflow", func(s *server, rr *httptest.ResponseRecorder) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows", nil)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Tenant-ID", "default")
			s.handleCreateWorkflow(rr, req)
		}},
		{"AcquireLock", func(s *server, rr *httptest.ResponseRecorder) {
			req := adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/locks/acquire", nil))
			req.Header.Set("Content-Type", "application/json")
			s.handleAcquireLock(rr, req)
		}},
		{"SubmitJob", func(s *server, rr *httptest.ResponseRecorder) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", nil)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Tenant-ID", "default")
			s.handleSubmitJobHTTP(rr, req)
		}},
		{"Login", func(s *server, rr *httptest.ResponseRecorder) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
			req.Header.Set("Content-Type", "application/json")
			s.handleLogin(rr, req)
		}},
	}
	for _, h := range handlers {
		t.Run(h.name, func(t *testing.T) {
			s, _, _ := newTestGateway(t)
			rr := httptest.NewRecorder()
			panicSafeCall(t, h.name+"/nil_body", func() {
				h.call(s, rr)
			})
			// Should return 4xx, not 5xx or panic
			assert.GreaterOrEqual(t, rr.Code, 400)
			assertBoundedErrorEnvelope(t, rr, h.name+"/nil_body")
		})
	}
}

// ---------------------------------------------------------------------------
// Path parameter injection resilience
// ---------------------------------------------------------------------------

func TestPanicResilience_PathParameterInjection(t *testing.T) {
	maliciousPaths := []struct {
		name  string
		value string
	}{
		{"empty", ""},
		{"traversal", "../../../etc/passwd"},
		{"huge", strings.Repeat("a", 10000)},
		{"null_byte", "\x00null"},
		{"xss", "<script>"},
		{"sqli", "'; DROP TABLE jobs;--"},
		{"encoded_null", "job%00id"},
		{"deep_traversal", strings.Repeat("../", 100)},
	}
	for _, tt := range maliciousPaths {
		t.Run("GetJob/"+tt.name, func(t *testing.T) {
			s, _, _ := newTestGateway(t)
			rr := httptest.NewRecorder()
			// Use a safe base URL and inject the hostile value only via SetPathValue,
			// because httptest.NewRequest panics on null bytes in URLs.
			req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/test-id", nil)
			req.SetPathValue("id", tt.value)
			req.Header.Set("X-Tenant-ID", "default")
			panicSafeCall(t, "GetJob/"+tt.name, func() {
				s.handleGetJob(rr, req)
			})
			assertBoundedErrorEnvelope(t, rr, "GetJob/"+tt.name)
		})
		t.Run("GetWorkflow/"+tt.name, func(t *testing.T) {
			s, _, _ := newTestGateway(t)
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/workflows/test-id", nil)
			req.SetPathValue("id", tt.value)
			req.Header.Set("X-Tenant-ID", "default")
			panicSafeCall(t, "GetWorkflow/"+tt.name, func() {
				s.handleGetWorkflow(rr, req)
			})
			assertBoundedErrorEnvelope(t, rr, "GetWorkflow/"+tt.name)
		})
	}
}

// ---------------------------------------------------------------------------
// Query parameter injection resilience
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// BUG-7: Validation error messages must truncate echoed user input
// ---------------------------------------------------------------------------

func TestTruncateForError(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		max    int
		expect string
	}{
		{"short input unchanged", "hello", 256, "hello"},
		{"exact limit unchanged", strings.Repeat("a", 256), 256, strings.Repeat("a", 256)},
		{"over limit truncated", strings.Repeat("b", 300), 256, strings.Repeat("b", 256) + "..."},
		{"1000 chars truncated", strings.Repeat("x", 1000), 256, strings.Repeat("x", 256) + "..."},
		{"empty string unchanged", "", 256, ""},
		{"zero max uses default 256", strings.Repeat("c", 300), 0, strings.Repeat("c", 256) + "..."},
		{"negative max uses default 256", strings.Repeat("d", 300), -1, strings.Repeat("d", 256) + "..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateForError(tt.input, tt.max)
			assert.Equal(t, tt.expect, result)
		})
	}
}

func TestValidateWorkflowStepID_OversizedInput_ErrorTruncated(t *testing.T) {
	// A 1000-character step ID should produce an error message well under 1041 chars.
	// Without truncation it would be ~1041 chars; with truncation at 256 the message
	// is ~300 chars (the format string adds ~40 chars of overhead around the 259-char
	// truncated+quoted value).
	longID := strings.Repeat("x", 1000)
	err := validateWorkflowStepID(longID)
	assert.Error(t, err, "expected error for oversized step ID")
	assert.LessOrEqual(t, len(err.Error()), 310,
		"error message should be at most 310 chars after truncation, got %d chars", len(err.Error()))
	assert.Contains(t, err.Error(), "...",
		"truncated error should contain ellipsis")
	assert.Contains(t, err.Error(), "exceeds",
		"error should mention the length violation")
}

func TestValidateWorkflowStepID_BadPattern_ErrorTruncated(t *testing.T) {
	// A 60-char step ID with invalid characters — under length limit but fails pattern.
	// The error message should still be bounded.
	badID := strings.Repeat("@", 60)
	err := validateWorkflowStepID(badID)
	assert.Error(t, err, "expected error for bad pattern")
	assert.Less(t, len(err.Error()), 300,
		"pattern error message should be under 300 chars")
	assert.Contains(t, err.Error(), "must match")
}

func TestValidateWorkflowStepID_ShortInput_NotTruncated(t *testing.T) {
	// A short invalid ID should appear in full (no "...").
	err := validateWorkflowStepID("bad@id")
	assert.Error(t, err)
	assert.NotContains(t, err.Error(), "...",
		"short input should not be truncated")
	assert.Contains(t, err.Error(), "bad@id",
		"short input should appear in full")
}

func TestPanicResilience_QueryParameterInjection(t *testing.T) {
	maliciousValues := []string{
		"",
		"-1",
		"0",
		"999999999999999999999999",
		"NaN",
		"Infinity",
		"-Infinity",
		"null",
		"true",
		"<script>alert(1)</script>",
		strings.Repeat("a", 10000),
		"'; DROP TABLE--",
	}
	for i, val := range maliciousValues {
		// URL-encode to prevent httptest.NewRequest from panicking on special chars.
		encoded := url.QueryEscape(val)
		t.Run(fmt.Sprintf("ListJobs/limit_%d", i), func(t *testing.T) {
			s, _, _ := newTestGateway(t)
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/jobs?limit=%s&offset=%s", encoded, encoded), nil)
			req.Header.Set("X-Tenant-ID", "default")
			panicSafeCall(t, fmt.Sprintf("ListJobs/query_%d", i), func() {
				s.handleListJobs(rr, req)
			})
			assertBoundedErrorEnvelope(t, rr, fmt.Sprintf("ListJobs/query_%d", i))
		})
		t.Run(fmt.Sprintf("ListWorkflows/limit_%d", i), func(t *testing.T) {
			s, _, _ := newTestGateway(t)
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/workflows?limit=%s", encoded), nil)
			req.Header.Set("X-Tenant-ID", "default")
			panicSafeCall(t, fmt.Sprintf("ListWorkflows/query_%d", i), func() {
				s.handleListWorkflows(rr, req)
			})
			assertBoundedErrorEnvelope(t, rr, fmt.Sprintf("ListWorkflows/query_%d", i))
		})
	}
}
