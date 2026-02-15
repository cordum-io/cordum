package gateway

import (
	"testing"
)

func TestSafeUnmarshal_ValidJSON(t *testing.T) {
	var tags []string
	ok := safeUnmarshal([]byte(`["pii","finance"]`), &tags, "risk_tags", "job-123")
	if !ok {
		t.Fatal("expected safeUnmarshal to return true for valid JSON")
	}
	if len(tags) != 2 || tags[0] != "pii" || tags[1] != "finance" {
		t.Fatalf("expected [pii finance], got %v", tags)
	}
}

func TestSafeUnmarshal_InvalidJSON(t *testing.T) {
	var tags []string
	ok := safeUnmarshal([]byte(`not-valid-json`), &tags, "risk_tags", "job-456")
	if ok {
		t.Fatal("expected safeUnmarshal to return false for invalid JSON")
	}
	if tags != nil {
		t.Fatalf("expected tags to remain nil, got %v", tags)
	}
}

func TestSafeUnmarshal_EmptyArray(t *testing.T) {
	var tags []string
	ok := safeUnmarshal([]byte(`[]`), &tags, "risk_tags", "job-789")
	if !ok {
		t.Fatal("expected safeUnmarshal to return true for empty array")
	}
	if len(tags) != 0 {
		t.Fatalf("expected empty slice, got %v", tags)
	}
}

func TestSafeUnmarshal_WrongType(t *testing.T) {
	var tags []string
	// Valid JSON but wrong type (object instead of array)
	ok := safeUnmarshal([]byte(`{"key":"val"}`), &tags, "risk_tags", "job-wrong")
	if ok {
		t.Fatal("expected safeUnmarshal to return false for type mismatch")
	}
}

func TestSafeUnmarshal_NestedObject(t *testing.T) {
	var result map[string]any
	ok := safeUnmarshal([]byte(`{"status":"ok","count":42}`), &result, "result_data", "job-nested")
	if !ok {
		t.Fatal("expected safeUnmarshal to return true for valid nested object")
	}
	if result["status"] != "ok" {
		t.Fatalf("expected status=ok, got %v", result["status"])
	}
}
