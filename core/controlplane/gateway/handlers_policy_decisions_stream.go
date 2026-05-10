package gateway

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/policy"
)

const (
	policyDecisionsStreamBufferSize = 64
	policyDecisionsStreamWriteWait  = 5 * time.Second
)

// handlePolicyDecisionsStream serves WebSocket clients on
// /api/v1/policy/decisions/stream. Each connected client subscribes to the
// gateway's in-process DecisionBroker; published decisions are written to
// the WS as a single JSON-encoded policy.Decision per message. Clients may
// pass `?source=job|edge` and `?type=<DecisionType>` to filter what reaches
// their socket; filtering happens after broker delivery so the broker stays
// shape-agnostic.
func (s *server) handlePolicyDecisionsStream(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermPolicyRead) {
		return
	}

	wantSource, wantType, err := parsePolicyDecisionsStreamFilters(r)
	if err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}

	conn, err := upgrader.Upgrade(w, r, negotiateSubprotocol(r))
	if err != nil {
		// Upgrade already wrote an HTTP response on failure; nothing else to do.
		return
	}

	broker := s.policyDecisionBroker()
	if broker == nil {
		_ = conn.Close()
		return
	}
	ch, unsubscribe := broker.Subscribe(policyDecisionsStreamBufferSize)
	if ch == nil {
		_ = conn.Close()
		return
	}

	// runCtx is cancelled once either the reader or the writer goroutine
	// exits, taking the other down with it.
	runCtx, cancel := context.WithCancel(r.Context())
	var closeOnce sync.Once
	closeBoth := func() {
		closeOnce.Do(func() {
			cancel()
			unsubscribe()
			_ = conn.Close()
		})
	}

	// Reader goroutine: discards client frames + detects disconnect via
	// ReadMessage error. We don't expect inbound traffic but must drain it
	// so the underlying TCP buffer doesn't stall the connection.
	go func() {
		defer closeBoth()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	// Writer loop runs on the request goroutine — handler returns when the
	// loop exits. Selecting on broker channel + context lets disconnect
	// propagate within one tick.
	for {
		select {
		case <-runCtx.Done():
			closeBoth()
			return
		case d, ok := <-ch:
			if !ok {
				// broker closed our subscription (slow consumer or shutdown)
				closeBoth()
				return
			}
			if !policyDecisionsStreamShouldDeliver(d, wantSource, wantType) {
				continue
			}
			payload, marshalErr := json.Marshal(d)
			if marshalErr != nil {
				slog.Warn("policy decisions stream marshal failed", "error", marshalErr)
				continue
			}
			if err := conn.SetWriteDeadline(time.Now().Add(policyDecisionsStreamWriteWait)); err != nil {
				closeBoth()
				return
			}
			if err := conn.WriteMessage(1 /* TextMessage */, payload); err != nil {
				closeBoth()
				return
			}
		}
	}
}

func parsePolicyDecisionsStreamFilters(r *http.Request) (policy.DecisionSource, policy.DecisionType, error) {
	var src policy.DecisionSource
	var dt policy.DecisionType

	if raw := strings.TrimSpace(r.URL.Query().Get("source")); raw != "" {
		parsed, err := policy.ParseDecisionSource(raw)
		if err != nil {
			return "", "", err
		}
		src = parsed
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("type")); raw != "" {
		parsed, err := policy.ParseDecisionType(raw)
		if err != nil {
			return "", "", err
		}
		dt = parsed
	}
	return src, dt, nil
}

func policyDecisionsStreamShouldDeliver(d policy.Decision, wantSource policy.DecisionSource, wantType policy.DecisionType) bool {
	if wantSource != "" && d.Source != wantSource {
		return false
	}
	if wantType != "" && d.Type != wantType {
		return false
	}
	return true
}
