package gateway

import (
	"encoding/json"
	"errors"
	"log/slog"
	"strings"

	edgecore "github.com/cordum/cordum/core/edge"
)

const edgeEventStreamType = "edge.event"

var errInvalidEdgeEventForStream = errors.New("invalid edge event for stream")

type edgeEventStreamEnvelope struct {
	Type        string                    `json:"type"`
	TenantID    string                    `json:"tenant_id"`
	SessionID   string                    `json:"session_id"`
	ExecutionID string                    `json:"execution_id"`
	Event       edgecore.AgentActionEvent `json:"event"`
}

func marshalEdgeEventEnvelope(event *edgecore.AgentActionEvent) ([]byte, error) {
	normalized, err := normalizeEdgeEventForStream(event)
	if err != nil {
		return nil, err
	}

	data, err := json.Marshal(edgeEventStreamEnvelope{
		Type:        edgeEventStreamType,
		TenantID:    normalized.TenantID,
		SessionID:   normalized.SessionID,
		ExecutionID: normalized.ExecutionID,
		Event:       normalized,
	})
	if err != nil {
		return nil, errors.New("marshal edge event stream envelope")
	}
	return data, nil
}

func normalizeEdgeEventForStream(event *edgecore.AgentActionEvent) (edgecore.AgentActionEvent, error) {
	if event == nil {
		return edgecore.AgentActionEvent{}, errInvalidEdgeEventForStream
	}

	normalized := *event
	normalized.TenantID = strings.TrimSpace(normalized.TenantID)
	normalized.SessionID = strings.TrimSpace(normalized.SessionID)
	normalized.ExecutionID = strings.TrimSpace(normalized.ExecutionID)
	normalized.EventID = strings.TrimSpace(normalized.EventID)
	if err := normalized.Validate(); err != nil {
		return edgecore.AgentActionEvent{}, errInvalidEdgeEventForStream
	}
	return normalized, nil
}

func (s *server) enqueueEdgeEvent(event edgecore.AgentActionEvent) (bool, error) {
	if s == nil {
		return false, errors.New("edge stream server required")
	}

	normalized, err := normalizeEdgeEventForStream(&event)
	if err != nil {
		return false, err
	}
	data, err := marshalEdgeEventEnvelope(&normalized)
	if err != nil {
		return false, err
	}
	return s.enqueueWSEvent(data, normalized.TenantID, ""), nil
}

func (s *server) forwardPersistedEdgeEvent(event edgecore.AgentActionEvent) {
	queued, err := s.enqueueEdgeEvent(event)
	if err != nil {
		// EDGE-014 step-11: marshal/normalize errors collapse to the
		// bounded marshal_error reason. We never put the raw error
		// string into a metric label.
		s.recordEdgeStreamDrop("marshal_error")
		slog.Warn("edge event stream enqueue dropped",
			"tenant_id", sanitizeUTF8ForLog(strings.TrimSpace(event.TenantID)),
			"session_id", sanitizeUTF8ForLog(strings.TrimSpace(event.SessionID)),
			"execution_id", sanitizeUTF8ForLog(strings.TrimSpace(event.ExecutionID)),
			"event_id", sanitizeUTF8ForLog(strings.TrimSpace(event.EventID)),
			"kind", sanitizeUTF8ForLog(strings.TrimSpace(string(event.Kind))),
			"error", err,
		)
		return
	}
	if !queued {
		// EDGE-014 step-11: the WS bridge couldn't accept the event —
		// the eventsCh buffer is full. The generic
		// cordum_gateway_ws_packets_dropped_total counter still fires
		// for the underlying WS surface; we add an Edge-specific
		// counter so dashboards can attribute Edge stream pressure
		// without joining against generic WS metrics.
		s.recordEdgeStreamDrop("client_buffer_full")
		slog.Warn("edge event stream queue full; persisted event was not broadcast",
			"tenant_id", sanitizeUTF8ForLog(strings.TrimSpace(event.TenantID)),
			"session_id", sanitizeUTF8ForLog(strings.TrimSpace(event.SessionID)),
			"execution_id", sanitizeUTF8ForLog(strings.TrimSpace(event.ExecutionID)),
			"event_id", sanitizeUTF8ForLog(strings.TrimSpace(event.EventID)),
			"kind", sanitizeUTF8ForLog(strings.TrimSpace(string(event.Kind))),
		)
		return
	}
	s.recordEdgeStreamEventSent(event.TenantID)
}

// recordEdgeStreamDrop fires the EDGE-014 stream-drop metric with a
// bounded reason. Nil-safe — if s.edgeRecorder is unset (test code
// that didn't call newServer), the call is silently ignored. The
// recorder's NormalizeStreamDropReason helper bounds the reason to
// the documented enum: marshal_error / client_buffer_full /
// tenant_filter / stopped (anything else collapses to 'other').
func (s *server) recordEdgeStreamDrop(reason string) {
	if s == nil || s.edgeRecorder == nil {
		return
	}
	s.edgeRecorder.RecordStreamDrop(reason)
}

func (s *server) recordEdgeStreamEventSent(tenant string) {
	if s == nil || s.edgeRecorder == nil {
		return
	}
	s.edgeRecorder.RecordStreamEventSent(tenant)
}
