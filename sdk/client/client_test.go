package client

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
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
