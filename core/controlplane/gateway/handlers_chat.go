package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/infra/logging"
	"github.com/cordum/cordum/core/infra/store"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	wf "github.com/cordum/cordum/core/workflow"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	chatDefaultLimit = 100
	// Keep in sync with core/contextwindow/engine defaultMaxHistory.
	chatMaxHistory = 20
)

type chatEvent struct {
	ID        string         `json:"id,omitempty"`
	Role      string         `json:"role,omitempty"`
	Content   string         `json:"content,omitempty"`
	Timestamp int64          `json:"ts,omitempty"`
	StepID    string         `json:"step_id,omitempty"`
	JobID     string         `json:"job_id,omitempty"`
	AgentID   string         `json:"agent_id,omitempty"`
	AgentName string         `json:"agent_name,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type chatMessage struct {
	ID        string         `json:"id"`
	RunID     string         `json:"run_id"`
	Role      string         `json:"role"`
	Content   string         `json:"content"`
	StepID    string         `json:"step_id,omitempty"`
	JobID     string         `json:"job_id,omitempty"`
	AgentID   string         `json:"agent_id,omitempty"`
	AgentName string         `json:"agent_name,omitempty"`
	CreatedAt string         `json:"created_at"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type chatResponse struct {
	Items      []chatMessage `json:"items"`
	NextCursor *int64        `json:"next_cursor,omitempty"`
}

type chatBusMessage struct {
	ID        string         `json:"id,omitempty"`
	RunID     string         `json:"runId,omitempty"`
	Role      string         `json:"role,omitempty"`
	Content   string         `json:"content,omitempty"`
	StepID    string         `json:"stepId,omitempty"`
	JobID     string         `json:"jobId,omitempty"`
	AgentID   string         `json:"agentId,omitempty"`
	AgentName string         `json:"agentName,omitempty"`
	CreatedAt string         `json:"createdAt,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type chatWSEnvelope struct {
	TraceId         string `json:"traceId,omitempty"`
	SenderId        string `json:"senderId,omitempty"`
	CreatedAt       string `json:"createdAt,omitempty"`
	ProtocolVersion int32  `json:"protocolVersion,omitempty"`
	Payload         struct {
		ChatMessage chatBusMessage `json:"chatMessage,omitempty"`
	} `json:"payload,omitempty"`
}

type chatSendRequest struct {
	Content   string         `json:"content"`
	Role      string         `json:"role,omitempty"`
	StepID    string         `json:"step_id,omitempty"`
	JobID     string         `json:"job_id,omitempty"`
	AgentID   string         `json:"agent_id,omitempty"`
	AgentName string         `json:"agent_name,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

func (s *server) handleGetRunChat(w http.ResponseWriter, r *http.Request) {
	if s.workflowStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "workflow store unavailable")
		return
	}
	if s.memStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "memory store unavailable")
		return
	}
	runID := r.PathValue("id")
	if runID == "" {
		writeErrorJSON(w, http.StatusBadRequest, "missing run id")
		return
	}

	run, err := s.workflowStore.GetRun(r.Context(), runID)
	if err != nil {
		writeErrorJSON(w, http.StatusNotFound, "not found")
		return
	}
	if err := s.requireTenantAccess(r, run.OrgID); err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}

	memoryID := runMemoryID(run)
	if memoryID == "" {
		writeErrorJSON(w, http.StatusBadRequest, "missing memory id")
		return
	}

	client, err := chatRedisClient(s.memStore)
	if err != nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	limit := parseChatLimit(r)
	cursor, hasCursor := parseChatCursor(r)
	key := chatHistoryKey(memoryID)
	total, err := client.LLen(r.Context(), key).Result()
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "failed to load chat history")
		return
	}
	if total == 0 {
		w.Header().Set("Content-Type", "application/json")
		writeJSON(w,chatResponse{Items: []chatMessage{}})
		return
	}

	end := total - 1
	if hasCursor {
		if cursor < 0 {
			cursor = 0
		}
		if cursor < total {
			end = cursor
		}
	}
	start := end - limit + 1
	if start < 0 {
		start = 0
	}

	items, err := client.LRange(r.Context(), key, start, end).Result()
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "failed to load chat history")
		return
	}
	if len(items) > 1 {
		for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
			items[i], items[j] = items[j], items[i]
		}
	}

	messages := make([]chatMessage, 0, len(items))
	for i, raw := range items {
		ev := chatEvent{}
		if err := json.Unmarshal([]byte(raw), &ev); err != nil {
			logging.Warn("chat", "corrupt history event, treating as system", "index", i, "error", err)
			trimmed := strings.TrimSpace(raw)
			if trimmed == "" {
				continue
			}
			ev.Role = "system"
			ev.Content = trimmed
		}
		idx := end - int64(i)
		fallbackID := runID + "-" + strconv.FormatInt(idx, 10)
		messages = append(messages, chatMessageFromEvent(runID, fallbackID, ev))
	}

	w.Header().Set("Content-Type", "application/json")
	var nextCursor *int64
	if start > 0 {
		nc := start - 1
		nextCursor = &nc
	}
	writeJSON(w,chatResponse{Items: messages, NextCursor: nextCursor})
}

func (s *server) handlePostRunChat(w http.ResponseWriter, r *http.Request) {
	if s.workflowStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "workflow store unavailable")
		return
	}
	if s.memStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "memory store unavailable")
		return
	}
	runID := r.PathValue("id")
	if runID == "" {
		writeErrorJSON(w, http.StatusBadRequest, "missing run id")
		return
	}

	run, err := s.workflowStore.GetRun(r.Context(), runID)
	if err != nil {
		writeErrorJSON(w, http.StatusNotFound, "not found")
		return
	}
	if err := s.requireTenantAccess(r, run.OrgID); err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}

	var body chatSendRequest
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeJSONDecodeError(w, err, "invalid body")
		return
	}
	content := strings.TrimSpace(body.Content)
	if content == "" {
		writeErrorJSON(w, http.StatusBadRequest, "content required")
		return
	}

	memoryID := runMemoryID(run)
	if memoryID == "" {
		writeErrorJSON(w, http.StatusBadRequest, "missing memory id")
		return
	}

	client, err := chatRedisClient(s.memStore)
	if err != nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	role := normalizeChatRole(body.Role)
	if role == "" {
		role = "user"
	}

	ev := chatEvent{
		ID:        uuid.NewString(),
		Role:      role,
		Content:   content,
		Timestamp: time.Now().UTC().Unix(),
		StepID:    strings.TrimSpace(body.StepID),
		JobID:     strings.TrimSpace(body.JobID),
		AgentID:   strings.TrimSpace(body.AgentID),
		AgentName: strings.TrimSpace(body.AgentName),
		Metadata:  body.Metadata,
	}
	data, err := json.Marshal(ev)
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "failed to encode chat message")
		return
	}

	key := chatHistoryKey(memoryID)
	if err := client.RPush(r.Context(), key, data).Err(); err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "failed to store chat message")
		return
	}
	if chatMaxHistory > 0 {
		_ = client.LTrim(r.Context(), key, -chatMaxHistory, -1).Err()
	}

	msg := chatMessageFromEvent(runID, ev.ID, ev)
	if s != nil {
		s.emitChatEvent(run, msg)
	}

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w,msg)
}

func (s *server) emitChatEvent(run *wf.WorkflowRun, msg chatMessage) {
	if s == nil || run == nil || msg.ID == "" {
		return
	}
	var env chatWSEnvelope
	env.TraceId = msg.ID
	env.SenderId = "api-gateway"
	env.CreatedAt = msg.CreatedAt
	env.ProtocolVersion = int32(capsdk.DefaultProtocolVersion)
	env.Payload.ChatMessage = chatBusMessage(msg)
	data, err := json.Marshal(env)
	if err != nil {
		logging.Error("api-gateway", "chat event marshal failed", "error", err)
		return
	}
	s.enqueueWSEvent(data, run.OrgID, "")
}

func runMemoryID(run *wf.WorkflowRun) string {
	if run == nil || run.ID == "" {
		return ""
	}
	if run.Input != nil {
		if raw, ok := run.Input["memory_id"]; ok {
			if s, ok := raw.(string); ok {
				if trimmed := store.NormalizeMemoryID(s); trimmed != "" {
					return trimmed
				}
			}
		}
	}
	return "run:" + run.ID
}

func chatHistoryKey(memoryID string) string {
	return "mem:" + memoryID + ":events"
}

func chatRedisClient(s store.Store) (redis.UniversalClient, error) {
	rs, ok := s.(*store.RedisStore)
	if !ok || rs.Client() == nil {
		return nil, errChatStoreUnavailable
	}
	return rs.Client(), nil
}

func parseChatLimit(r *http.Request) int64 {
	limit := int64(chatDefaultLimit)
	if q := r.URL.Query().Get("limit"); q != "" {
		if v, err := strconv.ParseInt(q, 10, 64); err == nil && v > 0 {
			limit = v
		}
	}
	if limit <= 0 {
		limit = chatDefaultLimit
	}
	if limit > 500 {
		limit = 500
	}
	return limit
}

func parseChatCursor(r *http.Request) (int64, bool) {
	if q := r.URL.Query().Get("cursor"); q != "" {
		if v, err := strconv.ParseInt(q, 10, 64); err == nil {
			return v, true
		}
	}
	return 0, false
}

func normalizeChatRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "user":
		return "user"
	case "assistant":
		return "agent"
	case "agent":
		return "agent"
	case "system":
		return "system"
	default:
		return ""
	}
}

func chatMessageFromEvent(runID, fallbackID string, ev chatEvent) chatMessage {
	id := strings.TrimSpace(ev.ID)
	if id == "" {
		id = fallbackID
	}
	role := normalizeChatRole(ev.Role)
	if role == "" {
		role = "agent"
	}
	createdAt := chatCreatedAt(ev.Timestamp)
	if createdAt == "" {
		createdAt = time.Now().UTC().Format(time.RFC3339)
	}
	return chatMessage{
		ID:        id,
		RunID:     runID,
		Role:      role,
		Content:   ev.Content,
		StepID:    strings.TrimSpace(ev.StepID),
		JobID:     strings.TrimSpace(ev.JobID),
		AgentID:   strings.TrimSpace(ev.AgentID),
		AgentName: strings.TrimSpace(ev.AgentName),
		CreatedAt: createdAt,
		Metadata:  ev.Metadata,
	}
}

func chatCreatedAt(ts int64) string {
	if ts <= 0 {
		return ""
	}
	switch {
	case ts > 1_000_000_000_000_000_000:
		return time.Unix(0, ts).UTC().Format(time.RFC3339)
	case ts > 1_000_000_000_000_000:
		return time.Unix(0, ts*1_000).UTC().Format(time.RFC3339)
	case ts > 1_000_000_000_000:
		return time.Unix(0, ts*1_000_000).UTC().Format(time.RFC3339)
	default:
		return time.Unix(ts, 0).UTC().Format(time.RFC3339)
	}
}

var errChatStoreUnavailable = errors.New("chat history unavailable: Redis store not configured")
