package logging

import (
	"bytes"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"testing"
)

func TestInfoTextFormat(t *testing.T) {
	logFormatOnce = sync.Once{}
	logAsJSON = false

	var buf bytes.Buffer
	origOut := log.Writer()
	origFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(origOut)
		log.SetFlags(origFlags)
	})

	Info("worker", "hello", "key", "val")
	got := strings.TrimSpace(buf.String())
	if !strings.Contains(got, "[WORKER] hello") || !strings.Contains(got, "key=val") {
		t.Fatalf("unexpected log output: %s", got)
	}
}

func TestErrorJSONFormat(t *testing.T) {
	logFormatOnce = sync.Once{}
	logAsJSON = false
	t.Setenv("CORDUM_LOG_FORMAT", "json")

	var buf bytes.Buffer
	origOut := log.Writer()
	origFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(origOut)
		log.SetFlags(origFlags)
	})

	Error("gateway", "boom", "code", 500)
	line := strings.TrimSpace(buf.String())
	var payload map[string]any
	if err := json.Unmarshal([]byte(line), &payload); err != nil {
		t.Fatalf("expected json output, got: %s", line)
	}
	if payload["level"] != "ERROR" || payload["component"] != "gateway" || payload["msg"] != "boom" {
		t.Fatalf("unexpected json payload: %#v", payload)
	}
}

func TestWarnTextFormat(t *testing.T) {
	logFormatOnce = sync.Once{}
	logAsJSON = false

	var buf bytes.Buffer
	origOut := log.Writer()
	origFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(origOut)
		log.SetFlags(origFlags)
	})

	Warn("worker", "slow", "key", "val")
	got := strings.TrimSpace(buf.String())
	if !strings.Contains(got, "[WORKER] WARN slow") || !strings.Contains(got, "key=val") {
		t.Fatalf("unexpected log output: %s", got)
	}
}

func TestFormatFields(t *testing.T) {
	out := formatFields("a", 1, "b")
	if !strings.Contains(out, "a=1") || !strings.Contains(out, "b=(missing)") {
		t.Fatalf("unexpected fields: %s", out)
	}
	out = formatFields()
	if out != "" {
		t.Fatalf("expected empty output")
	}
}

func TestSensitiveKeyRedaction(t *testing.T) {
	logFormatOnce = sync.Once{}
	logAsJSON = false

	var buf bytes.Buffer
	origOut := log.Writer()
	origFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(origOut)
		log.SetFlags(origFlags)
	})

	Info("test", "check redaction", "password", "s3cret", "user", "alice")
	got := buf.String()
	if strings.Contains(got, "s3cret") {
		t.Fatalf("sensitive value leaked into log output: %s", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("expected [REDACTED] in log output: %s", got)
	}
	if !strings.Contains(got, "user=alice") {
		t.Fatalf("non-sensitive value should appear: %s", got)
	}
}

func TestSensitiveKeyRedactionJSON(t *testing.T) {
	logFormatOnce = sync.Once{}
	logAsJSON = false
	t.Setenv("CORDUM_LOG_FORMAT", "json")

	var buf bytes.Buffer
	origOut := log.Writer()
	origFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(origOut)
		log.SetFlags(origFlags)
	})

	Error("test", "auth fail", "api_key", "tok_abc123", "status", 401)
	got := buf.String()
	if strings.Contains(got, "tok_abc123") {
		t.Fatalf("sensitive value leaked into JSON log: %s", got)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(got)), &payload); err != nil {
		t.Fatalf("expected json: %s", got)
	}
	fields, _ := payload["fields"].(map[string]any)
	if fields["api_key"] != "[REDACTED]" {
		t.Fatalf("expected api_key=[REDACTED], got: %v", fields["api_key"])
	}
}

func TestSensitiveKey(t *testing.T) {
	cases := []struct {
		key  string
		want bool
	}{
		{"password", true},
		{"user_password", true},
		{"api_key", true},
		{"apikey", true},
		{"secret", true},
		{"auth_token", true},
		{"credential", true},
		{"user", false},
		{"status", false},
		{"error", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := sensitiveKey(tc.key); got != tc.want {
			t.Errorf("sensitiveKey(%q) = %v, want %v", tc.key, got, tc.want)
		}
	}
}

func TestToString(t *testing.T) {
	if got := toString(" value\n"); got != " value\n" {
		t.Fatalf("unexpected string: %s", got)
	}
	if got := toString(123); got != "123" {
		t.Fatalf("unexpected non-string conversion: %s", got)
	}
}
