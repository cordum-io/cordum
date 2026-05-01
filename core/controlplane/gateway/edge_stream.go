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

func (s *server) enqueueEdgeEvent(event edgecore.AgentActionEvent) error {
	if s == nil {
		return errors.New("edge stream server required")
	}

	normalized, err := normalizeEdgeEventForStream(&event)
	if err != nil {
		return err
	}
	data, err := marshalEdgeEventEnvelope(&normalized)
	if err != nil {
		return err
	}
	s.enqueueWSEvent(data, normalized.TenantID, "")
	return nil
}

func (s *server) forwardPersistedEdgeEvent(event edgecore.AgentActionEvent) {
	if err := s.enqueueEdgeEvent(event); err != nil {
		slog.Warn("edge event stream enqueue dropped",
			"tenant_id", sanitizeUTF8ForLog(strings.TrimSpace(event.TenantID)),
			"session_id", sanitizeUTF8ForLog(strings.TrimSpace(event.SessionID)),
			"execution_id", sanitizeUTF8ForLog(strings.TrimSpace(event.ExecutionID)),
			"event_id", sanitizeUTF8ForLog(strings.TrimSpace(event.EventID)),
			"kind", sanitizeUTF8ForLog(strings.TrimSpace(string(event.Kind))),
			"error", err,
		)
	}
}
