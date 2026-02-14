package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestNormalizeAPIKeyTrimsQuotes(t *testing.T) {
	cases := map[string]string{
		"":              "",
		"test-api-key":  "test-api-key",
		"  test-key  ":  "test-key",
		"\"test-key\"":  "test-key",
		"'test-key'":    "test-key",
		" 'test-key' ":  "test-key",
		" \"test-key\"": "test-key",
	}
	for in, want := range cases {
		if got := normalizeAPIKey(in); got != want {
			t.Fatalf("normalizeAPIKey(%q)=%q want=%q", in, got, want)
		}
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
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set("Idempotency-Key", "abc")
	if got := idempotencyKeyFromRequest(req); got != "abc" {
		t.Fatalf("expected header key")
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/jobs?idempotency_key=xyz", nil)
	req.Header.Set("X-Tenant-ID", "default")
	if got := idempotencyKeyFromRequest(req); got != "xyz" {
		t.Fatalf("expected query key")
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
	if deriveMemoryIDFromReq("job.test", "mem:explicit", "job-1") != "explicit" {
		t.Fatalf("expected explicit memory id")
	}
	if deriveMemoryIDFromReq("job.test", "", "job-1") != "job-1" {
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
