package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/policy"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// startPolicyDecisionsStreamServer wires the real gateway routes onto a
// loopback httptest server. The returned *server is the production *server
// type; tests publish synthetic decisions through s.policyDecisionBroker()
// (added in step 5) and assert what the WS handler delivers.
func startPolicyDecisionsStreamServer(t *testing.T) (*server, *httptest.Server) {
	t.Helper()
	s, _, _ := newTestGateway(t)
	enableTestAuth(s)
	mux := http.NewServeMux()
	require.NoError(t, s.registerRoutes(mux))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return s, srv
}

func TestPolicyDecisionsStreamDeliversBrokerPublication(t *testing.T) {
	s, srv := startPolicyDecisionsStreamServer(t)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/policy/decisions/stream?tenant=default"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	// Wait for the handler to register a subscription before publishing.
	require.Eventually(t, func() bool { return s.policyDecisionBroker().SubscriberCount() >= 1 },
		2*time.Second, 25*time.Millisecond, "expected 1 broker subscriber")

	d := policy.Decision{
		Source: policy.DecisionSourceJob,
		RuleID: "stream-rule-1",
		Type:   policy.DecisionDeny,
	}
	s.policyDecisionBroker().Publish(context.Background(), d)

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(2*time.Second)))
	_, raw, err := conn.ReadMessage()
	require.NoError(t, err)
	var got policy.Decision
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Equal(t, "stream-rule-1", got.RuleID)
	require.Equal(t, policy.DecisionDeny, got.Type)
}

func TestPolicyDecisionsStreamDeliversToMultipleSubscribers(t *testing.T) {
	s, srv := startPolicyDecisionsStreamServer(t)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/policy/decisions/stream?tenant=default"

	connA, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = connA.Close() })
	connB, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = connB.Close() })

	require.Eventually(t, func() bool { return s.policyDecisionBroker().SubscriberCount() >= 2 },
		2*time.Second, 25*time.Millisecond, "expected 2 broker subscribers")

	d := policy.Decision{Source: policy.DecisionSourceEdge, RuleID: "fanout-1", Type: policy.DecisionAllow}
	s.policyDecisionBroker().Publish(context.Background(), d)

	for i, conn := range []*websocket.Conn{connA, connB} {
		require.NoError(t, conn.SetReadDeadline(time.Now().Add(2*time.Second)))
		_, raw, err := conn.ReadMessage()
		require.NoError(t, err, "subscriber %d", i)
		var got policy.Decision
		require.NoError(t, json.Unmarshal(raw, &got))
		require.Equal(t, "fanout-1", got.RuleID)
	}
}

func TestPolicyDecisionsStreamFiltersBySourceQueryParam(t *testing.T) {
	s, srv := startPolicyDecisionsStreamServer(t)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/policy/decisions/stream?tenant=default&source=edge"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	require.Eventually(t, func() bool { return s.policyDecisionBroker().SubscriberCount() >= 1 },
		2*time.Second, 25*time.Millisecond, "expected 1 broker subscriber")

	// Publish a non-matching job decision first; it must NOT arrive on the
	// edge-only client.
	jobOnly := policy.Decision{Source: policy.DecisionSourceJob, RuleID: "filter-out", Type: policy.DecisionAllow}
	s.policyDecisionBroker().Publish(context.Background(), jobOnly)

	// Then publish the matching edge decision.
	edgeMatch := policy.Decision{Source: policy.DecisionSourceEdge, RuleID: "filter-keep", Type: policy.DecisionDeny}
	s.policyDecisionBroker().Publish(context.Background(), edgeMatch)

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(2*time.Second)))
	_, raw, err := conn.ReadMessage()
	require.NoError(t, err)
	var got policy.Decision
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Equal(t, "filter-keep", got.RuleID, "stream must drop non-matching source")
}

func TestPolicyDecisionsStreamCleansUpOnClientDisconnect(t *testing.T) {
	s, srv := startPolicyDecisionsStreamServer(t)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/policy/decisions/stream?tenant=default"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)

	require.Eventually(t, func() bool { return s.policyDecisionBroker().SubscriberCount() >= 1 },
		2*time.Second, 25*time.Millisecond, "expected subscriber registered")

	require.NoError(t, conn.Close())

	require.Eventually(t, func() bool { return s.policyDecisionBroker().SubscriberCount() == 0 },
		3*time.Second, 50*time.Millisecond, "broker subscriber count should drop to 0 after disconnect")
}

func TestPolicyDecisionsStreamSlowSubscriberDoesNotBlockEmit(t *testing.T) {
	s, srv := startPolicyDecisionsStreamServer(t)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/policy/decisions/stream?tenant=default"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	require.Eventually(t, func() bool { return s.policyDecisionBroker().SubscriberCount() >= 1 },
		2*time.Second, 25*time.Millisecond, "expected subscriber registered")

	// Flood without reading; broker.Publish must stay non-blocking. Slow
	// client gets auto-unsubscribed; whole loop must complete in <1s.
	d := policy.Decision{Source: policy.DecisionSourceJob, RuleID: "slow-1", Type: policy.DecisionAllow}
	deadline := time.Now().Add(time.Second)
	for i := 0; i < 200; i++ {
		s.policyDecisionBroker().Publish(context.Background(), d)
		if time.Now().After(deadline) {
			t.Fatalf("Publish took too long; iteration %d", i)
		}
	}

	require.Eventually(t, func() bool { return s.policyDecisionBroker().SubscriberCount() == 0 },
		3*time.Second, 50*time.Millisecond, "broker should auto-unsubscribe slow client")
}
