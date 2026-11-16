package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cordum/cordum/core/edge"
)

// The Edge session that POST /api/v1/mcp/gateway/clients/connect creates, and
// its connected event, record only the principal a credential vouches for. A
// principal the caller merely asserted with X-Principal-Id is not recorded as
// their PrincipalID (task-a4604f76).
func TestMCPGatewayEvidenceOwner_AuthenticatedPrincipalOnly(t *testing.T) {
	const victim = "gateway-victim"
	cases := []struct {
		name, key, principal, owner string
		asserted                    bool
	}{
		{name: "asserted principal is not recorded", key: mcpAssertedUnboundKey, principal: victim, asserted: true},
		{name: "authenticated principal is recorded", key: mcpAssertedBoundKey, principal: mcpAssertedWorker, owner: mcpAssertedWorker},
	}
	mcpAssertedProfiles(t, func(t *testing.T, env map[string]string) {
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				s, p := mcpAssertedGateway(t, env, false)
				s.edgeStore = edge.NewRedisStoreFromClient(s.redisClient())
				_, _, _, connect := mcpGatewayHandlers(s)
				req := mcpAssertedRequest(http.MethodPost, "/api/v1/mcp/gateway/clients/connect", "", tc.key, victim)
				req = withAuth(req, mcpAssertedAuth(t, p, req, tc.principal, tc.asserted))
				rr := httptest.NewRecorder()
				connect(rr, req)
				var out struct {
					SessionID   string `json:"session_id"`
					ExecutionID string `json:"execution_id"`
				}
				if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &out) != nil || out.SessionID == "" {
					t.Fatalf("connect: status=%d body=%s, want 200 with a session", rr.Code, rr.Body.String())
				}
				ctx := context.Background()
				session, found, err := s.edgeStore.GetSession(ctx, "default", out.SessionID)
				if err != nil || !found {
					t.Fatalf("GetSession(%q): found=%v err=%v", out.SessionID, found, err)
				}
				if session.PrincipalID != tc.owner {
					t.Fatalf("session PrincipalID=%q, want %q", session.PrincipalID, tc.owner)
				}
				page, err := s.edgeStore.ListEvents(ctx, edge.ListEventsQuery{
					TenantID: "default", SessionID: out.SessionID, ExecutionID: out.ExecutionID, Limit: 10,
				})
				if err != nil || len(page.Items) != 1 {
					t.Fatalf("ListEvents: %d events, err=%v, want the connected event", len(page.Items), err)
				}
				if got := page.Items[0].PrincipalID; got != tc.owner {
					t.Fatalf("connected event PrincipalID=%q, want %q", got, tc.owner)
				}
			})
		}
	})
}
