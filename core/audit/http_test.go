package audit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSafeHTTPClient_BlocksExcessRedirects(t *testing.T) {
	// Server that always redirects to itself.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, r.URL.String(), http.StatusFound)
	}))
	defer srv.Close()

	client := safeHTTPClient(5 * time.Second)
	client.Transport = srv.Client().Transport // trust test TLS cert

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	_, err := client.Do(req)
	if err == nil {
		t.Fatal("expected error from excessive redirects")
	}
	if !strings.Contains(err.Error(), "stopped after") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSafeHTTPClient_BlocksNonHTTPS(t *testing.T) {
	// HTTPS server that redirects to an HTTP target.
	httpTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer httpTarget.Close()

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, httpTarget.URL, http.StatusFound)
	}))
	defer srv.Close()

	client := safeHTTPClient(5 * time.Second)
	client.Transport = srv.Client().Transport // trust test TLS cert

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	_, err := client.Do(req)
	if err == nil {
		t.Fatal("expected error from non-HTTPS redirect")
	}
	if !strings.Contains(err.Error(), "non-HTTPS") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSafeHTTPClient_AllowsHTTPS(t *testing.T) {
	// HTTPS server that responds normally (no redirect).
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := safeHTTPClient(5 * time.Second)
	client.Transport = srv.Client().Transport

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}
