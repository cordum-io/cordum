package audit

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestCloudWatchExporter_SequenceToken(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", "us-east-1")

	var mu sync.Mutex
	var tokens []string
	calls := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		token, _ := payload["sequenceToken"].(string)

		mu.Lock()
		tokens = append(tokens, token)
		calls++
		call := calls
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if call == 1 {
			if _, err := w.Write([]byte(`{"nextSequenceToken":"t1"}`)); err != nil {
				t.Fatalf("write response: %v", err)
			}
			return
		}
		if _, err := w.Write([]byte(`{"nextSequenceToken":"t2"}`)); err != nil {
			t.Fatalf("write response: %v", err)
		}
	}))
	defer srv.Close()

	exp, err := NewCloudWatchExporter("group", "stream", WithCloudWatchEndpoint(srv.URL))
	if err != nil {
		t.Fatalf("NewCloudWatchExporter: %v", err)
	}

	events := []SIEMEvent{{
		Timestamp: time.Now().UTC(),
		EventType: EventSafetyDecision,
		Severity:  SeverityInfo,
		TenantID:  "default",
		Action:    "test",
	}}
	if err := exp.Export(context.Background(), events); err != nil {
		t.Fatalf("Export first: %v", err)
	}
	if err := exp.Export(context.Background(), events); err != nil {
		t.Fatalf("Export second: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
	if len(tokens) != 2 {
		t.Fatalf("tokens = %d, want 2", len(tokens))
	}
	if tokens[0] != "" {
		t.Errorf("first sequenceToken = %q, want empty", tokens[0])
	}
	if tokens[1] != "t1" {
		t.Errorf("second sequenceToken = %q, want t1", tokens[1])
	}
}

func TestCloudWatchExporter_RetryOnInvalidSequenceToken(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", "us-east-1")

	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		token, _ := payload["sequenceToken"].(string)
		calls++

		switch calls {
		case 1:
			if token != "bad" {
				t.Errorf("first sequenceToken = %q, want bad", token)
			}
			w.WriteHeader(http.StatusBadRequest)
			if _, err := w.Write([]byte(`{"__type":"InvalidSequenceTokenException","expectedSequenceToken":"good"}`)); err != nil {
				t.Fatalf("write response: %v", err)
			}
			return
		case 2:
			if token != "good" {
				t.Errorf("second sequenceToken = %q, want good", token)
			}
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write([]byte(`{"nextSequenceToken":"next"}`)); err != nil {
				t.Fatalf("write response: %v", err)
			}
			return
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	exp, err := NewCloudWatchExporter("group", "stream", WithCloudWatchEndpoint(srv.URL))
	if err != nil {
		t.Fatalf("NewCloudWatchExporter: %v", err)
	}
	exp.sequenceToken = "bad"

	events := []SIEMEvent{{
		Timestamp: time.Now().UTC(),
		EventType: EventSafetyDecision,
		Severity:  SeverityInfo,
		TenantID:  "default",
		Action:    "retry",
	}}
	if err := exp.Export(context.Background(), events); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}
