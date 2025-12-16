package ollama

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestGenerateUsesServerResponse(t *testing.T) {
	srv := newIPv4Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response":"hello world"}`))
	}))
	defer srv.Close()

	os.Setenv("OLLAMA_URL", srv.URL)
	os.Setenv("OLLAMA_MODEL", "test-model")
	p := NewFromEnv()

	out, err := p.Generate(context.Background(), "test prompt")
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if out != "hello world" {
		t.Fatalf("expected response text, got %q", out)
	}
}

func TestGenerateEmptyPromptErrors(t *testing.T) {
	p := NewFromEnv()
	if _, err := p.Generate(context.Background(), ""); err == nil {
		t.Fatalf("expected error on empty prompt")
	}
}

func TestGenerateIncludesErrorBody(t *testing.T) {
	srv := newIPv4Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"model 'llama3' not found"}`))
	}))
	defer srv.Close()

	os.Setenv("OLLAMA_URL", srv.URL)
	os.Setenv("OLLAMA_MODEL", "llama3")
	p := NewFromEnv()

	_, err := p.Generate(context.Background(), "test prompt")
	if err == nil {
		t.Fatalf("expected error")
	}
	if got := err.Error(); got == "" || got != "ollama 404: model 'llama3' not found" {
		t.Fatalf("unexpected error: %q", got)
	}
}

func newIPv4Server(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()

	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("skipping: unable to listen on ipv4 loopback (%v)", err)
	}
	srv := httptest.NewUnstartedServer(handler)
	srv.Listener = ln
	srv.Start()
	return srv
}
