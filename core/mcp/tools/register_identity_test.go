package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGatewayClientFetchAgentIdentityCopiesMCPAllowlists(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/agents/agent-prod" {
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":                   "agent-prod",
			"allowed_servers":      []string{"prod-mcp"},
			"allowed_tools":        []string{"repo.*"},
			"allowed_resources":    []string{"cordum://repos/*"},
			"entitlements":         []string{"repo.read"},
			"risk_tier":            "high",
			"data_classifications": []string{"internal"},
			"status":               "active",
		})
	}))
	t.Cleanup(srv.Close)

	client := NewGatewayClient(srv.URL, "", srv.Client())
	got, err := client.FetchAgentIdentity(context.Background(), "agent-prod")
	if err != nil {
		t.Fatalf("FetchAgentIdentity: %v", err)
	}
	if got == nil {
		t.Fatal("FetchAgentIdentity returned nil")
	}
	requireStrings(t, "AllowedServers", got.AllowedServers, []string{"prod-mcp"})
	requireStrings(t, "AllowedTools", got.AllowedTools, []string{"repo.*"})
	requireStrings(t, "AllowedResources", got.AllowedResources, []string{"cordum://repos/*"})
	requireStrings(t, "Entitlements", got.Entitlements, []string{"repo.read"})
	requireStrings(t, "DataClassifications", got.DataClassifications, []string{"internal"})
}

func requireStrings(t *testing.T, name string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s length = %d (%v), want %d (%v)", name, len(got), got, len(want), want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s[%d] = %q, want %q", name, i, got[i], want[i])
		}
	}
}
