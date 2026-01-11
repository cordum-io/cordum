package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cordum/cordum/core/controlplane/scheduler"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestAllowedOriginsFromEnv(t *testing.T) {
	t.Setenv("CORDUM_ALLOWED_ORIGINS", "")
	t.Setenv("CORDUM_CORS_ALLOW_ORIGINS", "")
	t.Setenv("CORS_ALLOW_ORIGINS", "")
	allowed, allowAll := allowedOriginsFromEnv()
	if allowAll || allowed != nil {
		t.Fatalf("expected no allowed origins")
	}

	t.Setenv("CORDUM_ALLOWED_ORIGINS", "*")
	allowed, allowAll = allowedOriginsFromEnv()
	if !allowAll || allowed != nil {
		t.Fatalf("expected allow all origins")
	}

	t.Setenv("CORDUM_ALLOWED_ORIGINS", "https://example.com, http://localhost:3000")
	allowed, allowAll = allowedOriginsFromEnv()
	if allowAll {
		t.Fatalf("unexpected allow all")
	}
	if _, ok := allowed["https://example.com"]; !ok {
		t.Fatalf("missing example.com origin")
	}
	if _, ok := allowed["http://localhost:3000"]; !ok {
		t.Fatalf("missing localhost origin")
	}
}

func TestRequestHostname(t *testing.T) {
	if requestHostname("") != "" {
		t.Fatalf("expected empty hostname")
	}
	if requestHostname("example.com:8080") != "example.com" {
		t.Fatalf("expected host without port")
	}
	if requestHostname("example.com") != "example.com" {
		t.Fatalf("expected host unchanged")
	}
}

func TestParsePriority(t *testing.T) {
	cases := map[string]pb.JobPriority{
		"batch":       pb.JobPriority_JOB_PRIORITY_BATCH,
		"critical":    pb.JobPriority_JOB_PRIORITY_CRITICAL,
		"interactive": pb.JobPriority_JOB_PRIORITY_INTERACTIVE,
		"unknown":     pb.JobPriority_JOB_PRIORITY_INTERACTIVE,
	}
	for raw, expect := range cases {
		if got := parsePriority(raw); got != expect {
			t.Fatalf("priority %s expected %v got %v", raw, expect, got)
		}
	}
}

func TestParseBool(t *testing.T) {
	trues := []string{"1", "true", "yes", "y", "on"}
	for _, raw := range trues {
		if !parseBool(raw) {
			t.Fatalf("expected true for %s", raw)
		}
	}
	if parseBool("false") {
		t.Fatalf("expected false for false")
	}
}

func TestIdempotencyKeyFromRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	req.Header.Set("Idempotency-Key", "abc")
	if got := idempotencyKeyFromRequest(req); got != "abc" {
		t.Fatalf("expected header key")
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/jobs?idempotency_key=xyz", nil)
	if got := idempotencyKeyFromRequest(req); got != "xyz" {
		t.Fatalf("expected query key")
	}
}

func TestAddrFromEnv(t *testing.T) {
	t.Setenv("TEST_ADDR", "")
	if got := addrFromEnv("TEST_ADDR", "fallback"); got != "fallback" {
		t.Fatalf("expected fallback addr")
	}
	t.Setenv("TEST_ADDR", "127.0.0.1:9999")
	if got := addrFromEnv("TEST_ADDR", "fallback"); got != "127.0.0.1:9999" {
		t.Fatalf("expected env addr")
	}
}

func TestLoadAPIKeys(t *testing.T) {
	t.Setenv("CORDUM_SUPER_SECRET_API_TOKEN", "super")
	t.Setenv("CORDUM_API_KEY", "cordum")
	t.Setenv("API_KEY", "api")

	keys, required, err := loadBasicAPIKeys()
	if err != nil {
		t.Fatalf("load api keys: %v", err)
	}
	if !required {
		t.Fatalf("expected api key required")
	}
	if _, ok := keys["super"]; !ok {
		t.Fatalf("expected super secret key in key map")
	}

	t.Setenv("CORDUM_SUPER_SECRET_API_TOKEN", "")
	keys, _, err = loadBasicAPIKeys()
	if err != nil {
		t.Fatalf("load api keys: %v", err)
	}
	if _, ok := keys["cordum"]; !ok {
		t.Fatalf("expected cordum api key in key map")
	}

	t.Setenv("CORDUM_API_KEY", "")
	keys, _, err = loadBasicAPIKeys()
	if err != nil {
		t.Fatalf("load api keys: %v", err)
	}
	if _, ok := keys["api"]; !ok {
		t.Fatalf("expected api key in key map")
	}
}

func TestLookupIntPath(t *testing.T) {
	data := map[string]any{
		"limits": map[string]any{
			"int":     3,
			"int64":   int64(4),
			"float":   float64(5),
			"number":  json.Number("6"),
			"string":  "7",
			"bad_str": "nope",
		},
	}
	cases := map[string]int{
		"int":    3,
		"int64":  4,
		"float":  5,
		"number": 6,
		"string": 7,
	}
	for key, expect := range cases {
		if got := lookupIntPath(data, "limits", key); got != expect {
			t.Fatalf("key %s expected %d got %d", key, expect, got)
		}
	}
	if lookupIntPath(data, "limits", "bad_str") != 0 {
		t.Fatalf("expected bad string to return 0")
	}
	if lookupIntPath(data, "missing") != 0 {
		t.Fatalf("expected missing path to return 0")
	}
}

func TestParseContextModeAndMemoryID(t *testing.T) {
	if parseContextMode("job.test", "chat") != "chat" {
		t.Fatalf("expected chat mode")
	}
	if parseContextMode("job.test", "rag") != "rag" {
		t.Fatalf("expected rag mode")
	}
	if parseContextMode("job.test", "raw") != "raw" {
		t.Fatalf("expected raw mode")
	}
	if parseContextMode("job.test", "unknown") != "raw" {
		t.Fatalf("expected default raw mode")
	}
	if deriveMemoryIDFromReq("job.test", "mem:explicit", "job-1") != "mem:explicit" {
		t.Fatalf("expected explicit memory id")
	}
	if deriveMemoryIDFromReq("job.test", "", "job-1") != "mem:job-1" {
		t.Fatalf("expected derived memory id")
	}
}

func TestNormalizeTimestampHelpers(t *testing.T) {
	if got := normalizeTimestampMicrosLower(10); got != 10*microsPerSecond {
		t.Fatalf("unexpected micros lower: %d", got)
	}
	if got := normalizeTimestampMicrosUpper(10); got != 10*microsPerSecond+(microsPerSecond-1) {
		t.Fatalf("unexpected micros upper: %d", got)
	}
	if got := normalizeTimestampSecondsUpper(10); got != 10 {
		t.Fatalf("unexpected seconds upper: %d", got)
	}

	large := secondsThreshold + 5
	if got := normalizeTimestampMicrosLower(large); got != large*microsPerMillisecond {
		t.Fatalf("unexpected micros lower for millis")
	}
	if got := normalizeTimestampSecondsUpper(millisThreshold + 1000); got != (millisThreshold+1000)/1_000_000 {
		t.Fatalf("unexpected seconds upper for millis")
	}
}

func TestStatusRecorderWriteHeaderAndFlush(t *testing.T) {
	rr := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: rr}
	rec.WriteHeader(http.StatusTeapot)
	if rec.status != http.StatusTeapot {
		t.Fatalf("expected recorded status")
	}

	flusher := &flushWriter{ResponseWriter: rr}
	rec = &statusRecorder{ResponseWriter: flusher}
	rec.Flush()
	if !flusher.flushed {
		t.Fatalf("expected flush to be forwarded")
	}
}

type flushWriter struct {
	http.ResponseWriter
	flushed bool
}

func (f *flushWriter) Flush() {
	f.flushed = true
}

func TestCorsMiddleware(t *testing.T) {
	t.Setenv("CORDUM_ALLOWED_ORIGINS", "http://allowed.com")
	handler := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	req.Header.Set("Origin", "http://allowed.com")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected ok response, got %d", rr.Code)
	}
	if rr.Header().Get("Access-Control-Allow-Origin") != "http://allowed.com" {
		t.Fatalf("expected cors allow origin header")
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	req.Header.Set("Origin", "http://blocked.com")
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden response, got %d", rr.Code)
	}
}

func TestRateLimitMiddleware(t *testing.T) {
	orig := apiLimiter
	defer func() { apiLimiter = orig }()

	apiLimiter = &tokenBucket{tokens: make(chan struct{}, 1)}
	handler := rateLimitMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected rate limit response, got %d", rr.Code)
	}

	apiLimiter.tokens <- struct{}{}
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected ok response, got %d", rr.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/health", nil)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected health response, got %d", rr.Code)
	}
}

func TestHandleListJobDecisions(t *testing.T) {
	s, _, _ := newTestGateway(t)
	jobID := "job-decisions-1"
	record := scheduler.SafetyDecisionRecord{
		Decision:    scheduler.SafetyAllow,
		Reason:      "ok",
		Constraints: &pb.PolicyConstraints{RedactionLevel: "low"},
	}
	if err := s.jobStore.SetSafetyDecision(context.Background(), jobID, record); err != nil {
		t.Fatalf("set safety decision: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+jobID+"/decisions", nil)
	req.SetPathValue("id", jobID)
	rr := httptest.NewRecorder()
	s.handleListJobDecisions(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected ok response, got %d", rr.Code)
	}
	var out []scheduler.SafetyDecisionRecord
	if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(out) != 1 || out[0].Decision != scheduler.SafetyAllow {
		t.Fatalf("unexpected decisions: %#v", out)
	}
}

func TestSplitWorkflowJobID(t *testing.T) {
	run, step := splitWorkflowJobID("run-1:step-1")
	if run != "run-1" || step != "step-1" {
		t.Fatalf("unexpected split: %s %s", run, step)
	}
	run, step = splitWorkflowJobID("bad")
	if run != "" || step != "" {
		t.Fatalf("expected empty split for invalid id")
	}
}

func TestGatewaySafetyTransportCredentials(t *testing.T) {
	t.Setenv("SAFETY_KERNEL_TLS_CA", "")
	t.Setenv("SAFETY_KERNEL_INSECURE", "true")
	creds := safetyTransportCredentials()
	if creds == nil {
		t.Fatalf("expected credentials")
	}
	if creds.Info().SecurityProtocol != "insecure" {
		t.Fatalf("expected insecure credentials")
	}
}
