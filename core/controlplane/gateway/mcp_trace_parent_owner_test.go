package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/mcp"
)

// The Edge session and execution that mcpAuth creates for an HTTP MCP session
// are owned only by the principal a credential vouches for. A principal the
// caller merely asserted with X-Principal-Id is kept as an attribution label and
// never becomes an owner the runtime-ingest parent validator accepts
// (thread PRRT_kwDOQ30nx863accN).
func TestMCPTraceParentOwner_AuthenticatedPrincipalOnly(t *testing.T) {
	const victim = "collector-victim"
	cases := []struct {
		name, key, principal, owner, label string
		asserted                           bool
	}{
		{name: "asserted principal owns nothing", key: mcpAssertedUnboundKey, principal: victim, asserted: true, label: victim},
		{name: "authenticated principal owns its parents", key: mcpAssertedBoundKey, principal: mcpAssertedWorker, owner: mcpAssertedWorker},
	}
	mcpAssertedProfiles(t, func(t *testing.T, env map[string]string) {
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				s, p := mcpAssertedGateway(t, env, false)
				s.edgeStore = edge.NewRedisStoreFromClient(s.redisClient())
				req := mcpAssertedRequest(http.MethodPost, "/mcp/message", "", tc.key, victim)
				mcpAssertedAuth(t, p, req, tc.principal, tc.asserted)
				var meta mcp.CallMetadata
				rr := httptest.NewRecorder()
				s.mcpAuth(func(w http.ResponseWriter, r *http.Request) {
					meta, _ = mcp.CallMetadataFromContext(r.Context())
					w.WriteHeader(http.StatusNoContent)
				})(rr, req)
				if rr.Code != http.StatusNoContent || meta.SessionID == "" || meta.ExecutionID == "" {
					t.Fatalf("mcpAuth: status=%d session=%q execution=%q, want 204 and trace parents", rr.Code, meta.SessionID, meta.ExecutionID)
				}
				mcpCheckTraceParentOwner(t, s, meta, tc.owner, tc.label)
				validator := newRuntimeIngestParentValidator(s.edgeStore)
				ev := edge.AgentActionEvent{TenantID: "default", SessionID: meta.SessionID, ExecutionID: meta.ExecutionID}
				for _, collector := range []string{victim, mcpAssertedWorker} {
					err := validator.validate(context.Background(), ev, collector)
					if collector == tc.owner && err != nil {
						t.Fatalf("owner %q cannot ingest its own MCP trace: %v", collector, err)
					}
					if collector != tc.owner && !errors.Is(err, errRuntimeIngestCollectorParentDenied) {
						t.Fatalf("collector %q ingest into a trace owned by %q: err=%v, want the parent denial", collector, tc.owner, err)
					}
				}
			})
		}
	})
}

func mcpCheckTraceParentOwner(t *testing.T, s *server, meta mcp.CallMetadata, owner, label string) {
	t.Helper()
	ctx := context.Background()
	session, found, err := s.edgeStore.GetSession(ctx, "default", meta.SessionID)
	if err != nil || !found {
		t.Fatalf("GetSession(%q): found=%v err=%v", meta.SessionID, found, err)
	}
	execution, found, err := s.edgeStore.GetExecution(ctx, "default", meta.ExecutionID)
	if err != nil || !found {
		t.Fatalf("GetExecution(%q): found=%v err=%v", meta.ExecutionID, found, err)
	}
	if session.PrincipalID != owner || execution.WorkerID != owner {
		t.Fatalf("session PrincipalID=%q execution WorkerID=%q, want owner %q", session.PrincipalID, execution.WorkerID, owner)
	}
	if session.Labels["asserted_principal_id"] != label || execution.Labels["asserted_principal_id"] != label {
		t.Fatalf("asserted_principal_id session=%q execution=%q, want %q", session.Labels["asserted_principal_id"], execution.Labels["asserted_principal_id"], label)
	}
}
