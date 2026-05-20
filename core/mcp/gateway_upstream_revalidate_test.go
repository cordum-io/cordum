package mcp

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cordum/cordum/core/edge"
)

func TestMCPGatewayUpstreamPathRevalidatesPinnedIPs(t *testing.T) {
	prevLookup := edge.MCPHostLookup
	t.Cleanup(func() { edge.MCPHostLookup = prevLookup })
	edge.MCPHostLookup = func(_ context.Context, host string) ([]net.IP, error) {
		if host == "mcp.example.com" {
			return []net.IP{net.ParseIP("198.51.100.20")}, nil
		}
		return nil, &net.DNSError{Err: "no such host", Name: host}
	}

	store := &fakeGatewayStore{}
	deps := testDeps(store, true)
	deps.UpstreamRegistry = fakeGatewayUpstreamRegistry{items: []edge.UpstreamServer{{
		Name:        "tenant-tools",
		TenantID:    "tenant-a",
		Transport:   "http",
		Endpoint:    "https://mcp.example.com/tools",
		Risk:        "medium",
		Enabled:     true,
		ResolvedIPs: []string{"203.0.113.10"},
	}}}
	mux := newGatewayTestMux(t, deps)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp/gateway/upstream/connect", nil)
	req.Header.Set("X-Tenant-ID", "tenant-a")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("upstream path status = %d body=%q, want 502 for DNS pin mismatch", rr.Code, rr.Body.String())
	}
	assertJSONField(t, rr.Body.Bytes(), "code", "unsafe_upstream_endpoint")
	if len(store.sessions) != 1 || len(store.executions) != 1 || len(store.events) != 1 {
		t.Fatalf("evidence writes = sessions:%d executions:%d events:%d, want 1/1/1",
			len(store.sessions), len(store.executions), len(store.events))
	}
}

type fakeGatewayUpstreamRegistry struct {
	items []edge.UpstreamServer
}

func (f fakeGatewayUpstreamRegistry) Create(context.Context, *edge.UpstreamServer) error {
	return errors.New("fakeGatewayUpstreamRegistry: Create not expected")
}

func (f fakeGatewayUpstreamRegistry) Get(context.Context, string, string) (*edge.UpstreamServer, bool, error) {
	return nil, false, errors.New("fakeGatewayUpstreamRegistry: Get not expected")
}

func (f fakeGatewayUpstreamRegistry) List(_ context.Context, tenantID string) ([]edge.UpstreamServer, error) {
	out := make([]edge.UpstreamServer, 0, len(f.items))
	for _, item := range f.items {
		if item.TenantID == tenantID || item.TenantID == "*" {
			out = append(out, item)
		}
	}
	return out, nil
}

func (f fakeGatewayUpstreamRegistry) Update(context.Context, *edge.UpstreamServer) error {
	return errors.New("fakeGatewayUpstreamRegistry: Update not expected")
}

func (f fakeGatewayUpstreamRegistry) Disable(context.Context, string, string) error {
	return errors.New("fakeGatewayUpstreamRegistry: Disable not expected")
}

func (f fakeGatewayUpstreamRegistry) Enable(context.Context, string, string) error {
	return errors.New("fakeGatewayUpstreamRegistry: Enable not expected")
}
