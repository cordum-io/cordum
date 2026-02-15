package client

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
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

func TestApproveStepEncodesSegments(t *testing.T) {
	workflowID := "wf:1"
	runID := "run/1"
	stepID := "step:alpha/beta"
	expectedPath := "/api/v1/workflows/" + url.PathEscape(workflowID) +
		"/runs/" + url.PathEscape(runID) +
		"/steps/" + url.PathEscape(stepID) + "/approve"

	client := newTestClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.EscapedPath() != expectedPath {
			t.Fatalf("expected path %q, got %q", expectedPath, req.URL.EscapedPath())
		}
		return jsonResponse(http.StatusNoContent, ""), nil
	}))

	if err := client.ApproveStep(context.Background(), workflowID, runID, stepID, true); err != nil {
		t.Fatalf("ApproveStep error: %v", err)
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
	// BuildTLSTransport silently ignores read errors (returns transport with nil RootCAs).
	tr := BuildTLSTransport(TLSOptions{CACertPath: badCA})
	if tr == nil {
		t.Fatal("expected non-nil transport")
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
