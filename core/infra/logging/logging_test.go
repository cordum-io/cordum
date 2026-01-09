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

func TestToString(t *testing.T) {
	if got := toString(" value\n"); got != " value\n" {
		t.Fatalf("unexpected string: %s", got)
	}
	if got := toString(123); got != "123" {
		t.Fatalf("unexpected non-string conversion: %s", got)
	}
}
