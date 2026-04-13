package client

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func newTestClient(rt http.RoundTripper) *Client {
	return &Client{
		BaseURL:    "http://example.test",
		APIKey:     "test-key",
		TenantID:   "tenant-1",
		HTTPClient: &http.Client{Transport: rt},
	}
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func TestStartRunEncodesWorkflowID(t *testing.T) {
	workflowID := "wf/with:slash"
	expectedPath := "/api/v1/workflows/" + url.PathEscape(workflowID) + "/runs"

	client := newTestClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.EscapedPath() != expectedPath {
			t.Fatalf("expected path %q, got %q", expectedPath, req.URL.EscapedPath())
		}
		if req.URL.RawQuery != "dry_run=true" {
			t.Fatalf("expected dry_run query, got %q", req.URL.RawQuery)
		}
		return jsonResponse(http.StatusOK, `{"run_id":"run-1"}`), nil
	}))

	if _, err := client.StartRunWithDryRun(context.Background(), workflowID, map[string]any{"input": "ok"}, true); err != nil {
		t.Fatalf("StartRunWithDryRun error: %v", err)
	}
}

func TestGetArtifactEncodesPointer(t *testing.T) {
	ptr := "redis://job:1/ptr"
	expectedPath := "/api/v1/artifacts/" + url.PathEscape(ptr)
	payload := base64.StdEncoding.EncodeToString([]byte("ok"))

	client := newTestClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.EscapedPath() != expectedPath {
			t.Fatalf("expected path %q, got %q", expectedPath, req.URL.EscapedPath())
		}
		body := `{"artifact_ptr":"` + ptr + `","content_base64":"` + payload + `","metadata":{}}`
		return jsonResponse(http.StatusOK, body), nil
	}))

	artifact, err := client.GetArtifact(context.Background(), ptr)
	if err != nil {
		t.Fatalf("GetArtifact error: %v", err)
	}
	if artifact == nil || string(artifact.Content) != "ok" {
		t.Fatalf("expected artifact content, got %#v", artifact)
	}
}

func TestListTopics(t *testing.T) {
	client := newTestClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet {
			t.Fatalf("expected GET, got %s", req.Method)
		}
		if req.URL.EscapedPath() != "/api/v1/topics" {
			t.Fatalf("expected /api/v1/topics, got %q", req.URL.EscapedPath())
		}
		return jsonResponse(http.StatusOK, `{"items":[{"name":"job.alpha","pool":"pool-a","input_schema_id":"schema/in","output_schema_id":"schema/out","pack_id":"pack-a","requires":["docker"],"risk_tags":["external"],"status":"active","active_worker_count":2}]}`), nil
	}))

	topics, err := client.ListTopics(context.Background())
	if err != nil {
		t.Fatalf("ListTopics error: %v", err)
	}
	if len(topics) != 1 {
		t.Fatalf("expected 1 topic, got %d", len(topics))
	}
	if topics[0].Name != "job.alpha" || topics[0].Pool != "pool-a" {
		t.Fatalf("unexpected topic: %+v", topics[0])
	}
	if topics[0].InputSchemaID != "schema/in" || topics[0].OutputSchemaID != "schema/out" {
		t.Fatalf("unexpected schema bindings: %+v", topics[0])
	}
	if topics[0].ActiveWorkerCount != 2 {
		t.Fatalf("expected active worker count 2, got %d", topics[0].ActiveWorkerCount)
	}
}

func TestCreateTopic(t *testing.T) {
	input := TopicCreateInput{
		Name:           "job.pack.process",
		Pool:           "pack-pool",
		InputSchemaID:  "pack/Input",
		OutputSchemaID: "pack/Output",
		PackID:         "pack",
		Requires:       []string{"docker"},
		RiskTags:       []string{"external"},
		Status:         "active",
	}

	client := newTestClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", req.Method)
		}
		if req.URL.EscapedPath() != "/api/v1/topics" {
			t.Fatalf("expected /api/v1/topics, got %q", req.URL.EscapedPath())
		}
		var got TopicCreateInput
		if err := json.NewDecoder(req.Body).Decode(&got); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if got.Name != input.Name ||
			got.Pool != input.Pool ||
			got.InputSchemaID != input.InputSchemaID ||
			got.OutputSchemaID != input.OutputSchemaID ||
			got.PackID != input.PackID ||
			got.Status != input.Status ||
			strings.Join(got.Requires, ",") != strings.Join(input.Requires, ",") ||
			strings.Join(got.RiskTags, ",") != strings.Join(input.RiskTags, ",") {
			t.Fatalf("expected %#v, got %#v", input, got)
		}
		return jsonResponse(http.StatusCreated, ``), nil
	}))

	if err := client.CreateTopic(context.Background(), input); err != nil {
		t.Fatalf("CreateTopic error: %v", err)
	}
}

func TestDeleteTopicEncodesName(t *testing.T) {
	name := "job/topic:alpha"
	expectedPath := "/api/v1/topics/" + url.PathEscape(name)

	client := newTestClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodDelete {
			t.Fatalf("expected DELETE, got %s", req.Method)
		}
		if req.URL.EscapedPath() != expectedPath {
			t.Fatalf("expected path %q, got %q", expectedPath, req.URL.EscapedPath())
		}
		return jsonResponse(http.StatusNoContent, ``), nil
	}))

	if err := client.DeleteTopic(context.Background(), name); err != nil {
		t.Fatalf("DeleteTopic error: %v", err)
	}
}

func TestListWorkerCredentials(t *testing.T) {
	client := newTestClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet {
			t.Fatalf("expected GET, got %s", req.Method)
		}
		if req.URL.EscapedPath() != "/api/v1/workers/credentials" {
			t.Fatalf("expected /api/v1/workers/credentials, got %q", req.URL.EscapedPath())
		}
		return jsonResponse(http.StatusOK, `{"items":[{"worker_id":"worker-a","allowed_pools":["pool-a"],"allowed_topics":["job.alpha"],"pack_id":"pack-a","created_by":"admin","created_at":"2026-04-08T10:00:00Z","revoked_at":"2026-04-08T11:00:00Z"}]}`), nil
	}))

	records, err := client.ListWorkerCredentials(context.Background())
	if err != nil {
		t.Fatalf("ListWorkerCredentials error: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 credential, got %d", len(records))
	}
	if records[0].WorkerID != "worker-a" || records[0].CreatedBy != "admin" {
		t.Fatalf("unexpected credential: %+v", records[0])
	}
	if records[0].RevokedAt == "" {
		t.Fatalf("expected revoked credential metadata, got %+v", records[0])
	}
}

func TestCreateWorkerCredential(t *testing.T) {
	input := WorkerCredentialInput{
		WorkerID:      "worker-a",
		AllowedPools:  []string{"pool-a"},
		AllowedTopics: []string{"job.alpha"},
	}

	client := newTestClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", req.Method)
		}
		if req.URL.EscapedPath() != "/api/v1/workers/credentials" {
			t.Fatalf("expected /api/v1/workers/credentials, got %q", req.URL.EscapedPath())
		}
		var got WorkerCredentialInput
		if err := json.NewDecoder(req.Body).Decode(&got); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if got.WorkerID != input.WorkerID ||
			strings.Join(got.AllowedPools, ",") != strings.Join(input.AllowedPools, ",") ||
			strings.Join(got.AllowedTopics, ",") != strings.Join(input.AllowedTopics, ",") {
			t.Fatalf("expected %#v, got %#v", input, got)
		}
		return jsonResponse(http.StatusCreated, `{"worker_id":"worker-a","allowed_pools":["pool-a"],"allowed_topics":["job.alpha"],"created_by":"admin","created_at":"2026-04-08T10:00:00Z","token":"secret-token"}`), nil
	}))

	issued, err := client.CreateWorkerCredential(context.Background(), input)
	if err != nil {
		t.Fatalf("CreateWorkerCredential error: %v", err)
	}
	if issued == nil {
		t.Fatal("expected issued credential")
	}
	if issued.WorkerID != "worker-a" || issued.Token != "secret-token" {
		t.Fatalf("unexpected issued credential: %+v", issued)
	}
}

func TestRevokeWorkerCredentialEncodesWorkerID(t *testing.T) {
	workerID := "worker/a:1"
	expectedPath := "/api/v1/workers/credentials/" + url.PathEscape(workerID)

	client := newTestClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodDelete {
			t.Fatalf("expected DELETE, got %s", req.Method)
		}
		if req.URL.EscapedPath() != expectedPath {
			t.Fatalf("expected path %q, got %q", expectedPath, req.URL.EscapedPath())
		}
		return jsonResponse(http.StatusNoContent, ``), nil
	}))

	if err := client.RevokeWorkerCredential(context.Background(), workerID); err != nil {
		t.Fatalf("RevokeWorkerCredential error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// TLS tests
// ---------------------------------------------------------------------------

func TestBuildTLSTransportNil(t *testing.T) {
	tr := BuildTLSTransport(TLSOptions{})
	if tr != nil {
		t.Fatal("expected nil transport for zero-value TLSOptions")
	}
}

func TestBuildTLSTransportInsecure(t *testing.T) {
	tr := BuildTLSTransport(TLSOptions{InsecureSkipVerify: true})
	if tr == nil {
		t.Fatal("expected non-nil transport")
	}
	if tr.TLSClientConfig == nil {
		t.Fatal("expected TLSClientConfig")
	}
	if !tr.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("expected InsecureSkipVerify=true")
	}
}

func TestBuildTLSTransportWithCA(t *testing.T) {
	caPath := generateTestCA(t)
	tr := BuildTLSTransport(TLSOptions{CACertPath: caPath})
	if tr == nil {
		t.Fatal("expected non-nil transport")
	}
	if tr.TLSClientConfig == nil {
		t.Fatal("expected TLSClientConfig")
	}
	if tr.TLSClientConfig.RootCAs == nil {
		t.Fatal("expected RootCAs to be populated")
	}
}

func TestBuildTLSTransportBadCA(t *testing.T) {
	dir := t.TempDir()
	badCA := filepath.Join(dir, "bad.crt")
	if err := os.WriteFile(badCA, []byte("not a cert"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	// BuildTLSTransportErr returns an error for invalid PEM content.
	tr, err := BuildTLSTransportErr(TLSOptions{CACertPath: badCA})
	if err == nil {
		t.Fatal("expected error for invalid PEM content")
	}
	if tr != nil {
		t.Fatal("expected nil transport on error")
	}
	if !strings.Contains(err.Error(), "no valid PEM") {
		t.Fatalf("expected PEM parse error, got: %v", err)
	}
}

func TestNewWithTLSAppliesTransport(t *testing.T) {
	caPath := generateTestCA(t)
	client := NewWithTLS("https://example.com", "key", TLSOptions{CACertPath: caPath})
	if client.HTTPClient == nil {
		t.Fatal("expected HTTPClient")
	}
	if client.HTTPClient.Transport == nil {
		t.Fatal("expected custom Transport when CA path provided")
	}
}

func TestNewWithTLSNoCustomTransport(t *testing.T) {
	client := NewWithTLS("https://example.com", "key", TLSOptions{})
	if client.HTTPClient == nil {
		t.Fatal("expected HTTPClient")
	}
	if client.HTTPClient.Transport != nil {
		t.Fatal("expected nil Transport for zero TLSOptions")
	}
}

// generateTestCA creates a self-signed CA certificate and returns the path.
func generateTestCA(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ca key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test CA"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("ca cert: %v", err)
	}
	path := filepath.Join(dir, "ca.crt")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()
	if err := pem.Encode(f, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		t.Fatalf("pem encode: %v", err)
	}
	return path
}

// faultyReadCloser returns partial data then an error on Read.
type faultyReadCloser struct {
	partial string
	readErr error
	read    bool
}

func (f *faultyReadCloser) Read(p []byte) (int, error) {
	if !f.read {
		f.read = true
		n := copy(p, f.partial)
		return n, f.readErr
	}
	return 0, f.readErr
}

func (f *faultyReadCloser) Close() error { return nil }

// TestDoJSONBodyReadErrorPreserved verifies that when reading a non-2xx
// response body fails, the read error is included in the returned error
// instead of being silently dropped.
func TestDoJSONBodyReadErrorPreserved(t *testing.T) {
	readErr := fmt.Errorf("connection reset by peer")
	client := newTestClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 500,
			Body:       &faultyReadCloser{partial: "", readErr: readErr},
			Header:     make(http.Header),
		}, nil
	}))

	err := client.doJSON(context.Background(), http.MethodGet, "/test", nil, nil)
	if err == nil {
		t.Fatal("expected error for non-2xx response")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, "500") {
		t.Fatalf("expected status code in error, got: %s", errStr)
	}
	if !strings.Contains(errStr, "body read error") {
		t.Fatalf("expected body read error context, got: %s", errStr)
	}
	if !strings.Contains(errStr, "connection reset") {
		t.Fatalf("expected original read error, got: %s", errStr)
	}
}

// TestDoJSONBodyReadErrorWithPartialBody verifies that partial body content
// is still included alongside the read error.
func TestDoJSONBodyReadErrorWithPartialBody(t *testing.T) {
	client := newTestClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 502,
			Body:       &faultyReadCloser{partial: "Bad Gateway from", readErr: fmt.Errorf("truncated")},
			Header:     make(http.Header),
		}, nil
	}))

	err := client.doJSON(context.Background(), http.MethodGet, "/test", nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, "502") {
		t.Fatalf("expected status code, got: %s", errStr)
	}
	if !strings.Contains(errStr, "Bad Gateway from") {
		t.Fatalf("expected partial body in error, got: %s", errStr)
	}
	if !strings.Contains(errStr, "truncated") {
		t.Fatalf("expected read error, got: %s", errStr)
	}
}

// TestDoJSONNon2xxNoReadError verifies the normal non-2xx path still works
// when body read succeeds.
func TestDoJSONNon2xxNoReadError(t *testing.T) {
	client := newTestClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return jsonResponse(403, `{"error":"forbidden"}`), nil
	}))

	err := client.doJSON(context.Background(), http.MethodGet, "/test", nil, nil)
	if err == nil {
		t.Fatal("expected error for 403")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, "403") {
		t.Fatalf("expected 403, got: %s", errStr)
	}
	if !strings.Contains(errStr, "forbidden") {
		t.Fatalf("expected body message, got: %s", errStr)
	}
	if strings.Contains(errStr, "body read error") {
		t.Fatalf("should not contain body read error for successful read, got: %s", errStr)
	}
}

// ---------------------------------------------------------------------------
// Regression: TLS MinVersion enforcement
// ---------------------------------------------------------------------------

func TestBuildTLSTransportErrMinVersion(t *testing.T) {
	// Insecure mode should still enforce TLS 1.2 minimum.
	tr, err := BuildTLSTransportErr(TLSOptions{InsecureSkipVerify: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tr == nil {
		t.Fatal("expected non-nil transport")
	}
	if tr.TLSClientConfig.MinVersion != 0x0303 { // tls.VersionTLS12
		t.Fatalf("expected MinVersion=TLS1.2 (0x0303), got 0x%04x", tr.TLSClientConfig.MinVersion)
	}
}

func TestBuildTLSTransportErrWithCAMinVersion(t *testing.T) {
	caPath := generateTestCA(t)
	tr, err := BuildTLSTransportErr(TLSOptions{CACertPath: caPath})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tr == nil {
		t.Fatal("expected non-nil transport")
	}
	if tr.TLSClientConfig.MinVersion != 0x0303 {
		t.Fatalf("expected MinVersion=TLS1.2, got 0x%04x", tr.TLSClientConfig.MinVersion)
	}
}

// ---------------------------------------------------------------------------
// Regression: CA file read errors are reported, not silently swallowed
// ---------------------------------------------------------------------------

func TestBuildTLSTransportErrMissingCA(t *testing.T) {
	tr, err := BuildTLSTransportErr(TLSOptions{CACertPath: "/nonexistent/ca.crt"})
	if err == nil {
		t.Fatal("expected error for missing CA file")
	}
	if tr != nil {
		t.Fatal("expected nil transport on error")
	}
	if !strings.Contains(err.Error(), "read ca cert") {
		t.Fatalf("expected descriptive error, got: %v", err)
	}
}

func TestBuildTLSTransportErrNoOptions(t *testing.T) {
	tr, err := BuildTLSTransportErr(TLSOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tr != nil {
		t.Fatal("expected nil transport for zero-value TLSOptions")
	}
}

// ---------------------------------------------------------------------------
// Regression: NewWithTLSErr strict error propagation
// ---------------------------------------------------------------------------

func TestNewWithTLSErrMissingCA(t *testing.T) {
	_, err := NewWithTLSErr("https://example.com", "key", TLSOptions{CACertPath: "/nonexistent/ca.crt"})
	if err == nil {
		t.Fatal("expected error for missing CA file")
	}
	if !strings.Contains(err.Error(), "tls config") {
		t.Fatalf("expected tls config error wrapper, got: %v", err)
	}
}

func TestNewWithTLSErrSuccess(t *testing.T) {
	c, err := NewWithTLSErr("https://example.com", "key", TLSOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	if c.BaseURL != "https://example.com" {
		t.Fatalf("expected base url, got %s", c.BaseURL)
	}
}

// ---------------------------------------------------------------------------
// Regression: Secret redaction in error responses
// ---------------------------------------------------------------------------

func TestRedactSecretsInErrorBody(t *testing.T) {
	apiKey := "super-secret-api-key-1234"
	client := &Client{
		BaseURL:  "http://example.test",
		APIKey:   apiKey,
		TenantID: "tenant-1",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			// Server echoes back the API key in the error response.
			body := fmt.Sprintf(`{"error":"invalid key: %s"}`, apiKey)
			return jsonResponse(401, body), nil
		})},
	}

	err := client.doJSON(context.Background(), http.MethodGet, "/test", nil, nil)
	if err == nil {
		t.Fatal("expected error for 401")
	}
	errStr := err.Error()
	if strings.Contains(errStr, apiKey) {
		t.Fatalf("API key should be redacted from error, got: %s", errStr)
	}
	if !strings.Contains(errStr, "[REDACTED]") {
		t.Fatalf("expected [REDACTED] placeholder, got: %s", errStr)
	}
}

func TestRedactSecretsShortKey(t *testing.T) {
	// Short API keys (< 8 chars) are not redacted to avoid false positives.
	client := &Client{
		BaseURL:  "http://example.test",
		APIKey:   "short",
		TenantID: "tenant-1",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return jsonResponse(401, `error contains short`), nil
		})},
	}

	err := client.doJSON(context.Background(), http.MethodGet, "/test", nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	// "short" should NOT be redacted because the key is too short.
	if strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatal("short keys should not trigger redaction")
	}
}

// ---------------------------------------------------------------------------
// Regression: Client.String() redacts API key
// ---------------------------------------------------------------------------

func TestClientStringRedactsKey(t *testing.T) {
	c := &Client{
		BaseURL:  "https://example.com",
		APIKey:   "super-secret-api-key",
		TenantID: "default",
	}
	s := c.String()
	if strings.Contains(s, "super-secret-api-key") {
		t.Fatalf("String() should redact API key, got: %s", s)
	}
	if !strings.Contains(s, "supe****") {
		t.Fatalf("expected partial key prefix, got: %s", s)
	}
}

func TestClientStringNoKey(t *testing.T) {
	c := &Client{BaseURL: "https://example.com", TenantID: "default"}
	s := c.String()
	if !strings.Contains(s, "(none)") {
		t.Fatalf("expected (none) for empty key, got: %s", s)
	}
}

// ---------------------------------------------------------------------------
// Regression: Empty/invalid URL handling
// ---------------------------------------------------------------------------

func TestDoJSONEmptyBaseURL(t *testing.T) {
	client := &Client{
		BaseURL:  "",
		APIKey:   "key",
		TenantID: "t",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("should not reach here")
		})},
	}
	// Empty base URL produces an invalid request URL.
	err := client.doJSON(context.Background(), http.MethodGet, "/test", nil, nil)
	if err == nil {
		t.Fatal("expected error for empty base URL")
	}
}

// ---------------------------------------------------------------------------
// Regression: Auth header injection
// ---------------------------------------------------------------------------

func TestDoJSONSetsAuthHeaders(t *testing.T) {
	client := &Client{
		BaseURL:  "http://example.test",
		APIKey:   "my-key",
		TenantID: "my-tenant",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if got := req.Header.Get("X-API-Key"); got != "my-key" {
				t.Fatalf("expected X-API-Key header, got %q", got)
			}
			if got := req.Header.Get("X-Tenant-ID"); got != "my-tenant" {
				t.Fatalf("expected X-Tenant-ID header, got %q", got)
			}
			return jsonResponse(200, `{}`), nil
		})},
	}
	err := client.doJSON(context.Background(), http.MethodGet, "/test", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDoJSONNoAuthHeaderWhenEmpty(t *testing.T) {
	client := &Client{
		BaseURL:  "http://example.test",
		APIKey:   "",
		TenantID: "",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if got := req.Header.Get("X-API-Key"); got != "" {
				t.Fatalf("expected no X-API-Key header, got %q", got)
			}
			if got := req.Header.Get("X-Tenant-ID"); got != "" {
				t.Fatalf("expected no X-Tenant-ID header, got %q", got)
			}
			return jsonResponse(200, `{}`), nil
		})},
	}
	err := client.doJSON(context.Background(), http.MethodGet, "/test", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Regression: Nil request validation
// ---------------------------------------------------------------------------

func TestCreateWorkflowNilRequest(t *testing.T) {
	client := newTestClient(nil)
	_, err := client.CreateWorkflow(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil request")
	}
}

func TestSubmitJobNilRequest(t *testing.T) {
	client := newTestClient(nil)
	_, err := client.SubmitJob(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil request")
	}
}

func TestGetRunEmptyID(t *testing.T) {
	client := newTestClient(nil)
	_, err := client.GetRun(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty run ID")
	}
}

func TestApproveJobEmptyID(t *testing.T) {
	client := newTestClient(nil)
	err := client.ApproveJob(context.Background(), "", true)
	if err == nil {
		t.Fatal("expected error for empty job ID")
	}
}

func TestRepairApprovalEmptyID(t *testing.T) {
	client := newTestClient(nil)
	if _, err := client.RepairApproval(context.Background(), "", false, ""); err == nil {
		t.Fatal("expected error for empty job ID")
	}
}

func TestRepairApprovalEncodesRequest(t *testing.T) {
	client := newTestClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", req.Method)
		}
		if req.URL.EscapedPath() != "/api/v1/approvals/job-1/repair" {
			t.Fatalf("unexpected path %q", req.URL.EscapedPath())
		}
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if apply, _ := body["apply"].(bool); !apply {
			t.Fatalf("expected apply=true, got %#v", body)
		}
		if note, _ := body["note"].(string); note != "operator fix" {
			t.Fatalf("expected note in request body, got %#v", body)
		}
		return jsonResponse(http.StatusOK, `{"job_id":"job-1","applied":true}`), nil
	}))

	resp, err := client.RepairApproval(context.Background(), "job-1", true, "operator fix")
	if err != nil {
		t.Fatalf("RepairApproval error: %v", err)
	}
	if applied, _ := resp["applied"].(bool); !applied {
		t.Fatalf("expected applied response, got %#v", resp)
	}
}

// ---------------------------------------------------------------------------
// Regression: StartRunWithOptions sends input correctly
// ---------------------------------------------------------------------------

func TestStartRunWithOptionsEncodesInput(t *testing.T) {
	input := map[string]any{
		"date_range": map[string]any{
			"start": "2026-03-08",
			"end":   "2026-03-15",
		},
	}
	client := newTestClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(body, &decoded); err != nil {
			t.Fatalf("unmarshal body: %v", err)
		}
		dr, ok := decoded["date_range"].(map[string]any)
		if !ok {
			t.Fatalf("expected date_range object in body, got: %s", string(body))
		}
		if dr["start"] != "2026-03-08" || dr["end"] != "2026-03-15" {
			t.Fatalf("unexpected date_range values: %v", dr)
		}
		return jsonResponse(http.StatusOK, `{"run_id":"run-42"}`), nil
	}))

	runID, err := client.StartRunWithOptions(context.Background(), "test-wf", input, RunOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if runID != "run-42" {
		t.Fatalf("expected run-42, got %s", runID)
	}
}

// ---------------------------------------------------------------------------
// Regression: GetRun deserializes Input field
// ---------------------------------------------------------------------------

func TestGetRunIncludesInput(t *testing.T) {
	serverResp := `{
		"id": "run-123",
		"workflow_id": "wf-1",
		"status": "running",
		"input": {"date_range": {"start": "last_24h", "end": "now"}, "count": 5}
	}`
	client := newTestClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, serverResp), nil
	}))

	run, err := client.GetRun(context.Background(), "run-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.Input == nil {
		t.Fatal("expected Input to be populated, got nil")
	}
	dr, ok := run.Input["date_range"].(map[string]any)
	if !ok {
		t.Fatalf("expected date_range map in Input, got: %v", run.Input)
	}
	if dr["start"] != "last_24h" || dr["end"] != "now" {
		t.Fatalf("unexpected date_range values: %v", dr)
	}
	if run.Input["count"] != float64(5) {
		t.Fatalf("expected count=5, got: %v", run.Input["count"])
	}
}

func TestGetRunEmptyInput(t *testing.T) {
	serverResp := `{"id": "run-456", "workflow_id": "wf-2", "status": "pending", "input": {}}`
	client := newTestClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, serverResp), nil
	}))

	run, err := client.GetRun(context.Background(), "run-456")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Input with omitempty: empty map should still deserialize (JSON has "input": {})
	if run.Input == nil {
		t.Fatal("expected Input to be non-nil for empty object")
	}
	if len(run.Input) != 0 {
		t.Fatalf("expected empty Input map, got: %v", run.Input)
	}
}

func TestStartRunWithOptionsNilInputSendsEmptyObject(t *testing.T) {
	client := newTestClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		trimmed := strings.TrimSpace(string(body))
		if trimmed != "{}" {
			t.Fatalf("expected empty object {}, got: %s", trimmed)
		}
		return jsonResponse(http.StatusOK, `{"run_id":"run-nil"}`), nil
	}))

	runID, err := client.StartRunWithOptions(context.Background(), "test-wf", nil, RunOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if runID != "run-nil" {
		t.Fatalf("expected run-nil, got %s", runID)
	}
}
