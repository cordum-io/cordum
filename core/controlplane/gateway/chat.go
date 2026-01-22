package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/infra/memory"
	wf "github.com/cordum/cordum/core/workflow"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	chatDefaultLimit = 100
	// Keep in sync with core/context/engine defaultMaxHistory.
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
		http.Error(w, "workflow store unavailable", http.StatusServiceUnavailable)
		return
	}
	if s.memStore == nil {
		http.Error(w, "memory store unavailable", http.StatusServiceUnavailable)
		return
	}
	runID := r.PathValue("id")
	if runID == "" {
		http.Error(w, "missing run id", http.StatusBadRequest)
		return
	}

	run, err := s.workflowStore.GetRun(r.Context(), runID)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err := s.requireTenantAccess(r, run.OrgID); err != nil {
		http.Error(w, "tenant access denied", http.StatusForbidden)
		return
	}

	memoryID := runMemoryID(run)
	if memoryID == "" {
		http.Error(w, "missing memory id", http.StatusBadRequest)
		return
	}

	client, err := chatRedisClient(s.memStore)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotImplemented)
		return
	}

	limit := parseChatLimit(r)
	key := chatHistoryKey(memoryID)
	items, err := client.LRange(r.Context(), key, -limit, -1).Result()
	if err != nil {
		http.Error(w, "failed to load chat history", http.StatusInternalServerError)
		return
	}

	messages := make([]chatMessage, 0, len(items))
	for i, raw := range items {
		ev := chatEvent{}
		if json.Unmarshal([]byte(raw), &ev) != nil {
			trimmed := strings.TrimSpace(raw)
			if trimmed == "" {
				continue
			}
			ev.Role = "system"
			ev.Content = trimmed
		}
		fallbackID := runID + "-" + strconv.Itoa(i)
		messages = append(messages, chatMessageFromEvent(runID, fallbackID, ev))
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(chatResponse{Items: messages})
}

func (s *server) handlePostRunChat(w http.ResponseWriter, r *http.Request) {
	if s.workflowStore == nil {
		http.Error(w, "workflow store unavailable", http.StatusServiceUnavailable)
		return
	}
	if s.memStore == nil {
		http.Error(w, "memory store unavailable", http.StatusServiceUnavailable)
		return
	}
	runID := r.PathValue("id")
	if runID == "" {
		http.Error(w, "missing run id", http.StatusBadRequest)
		return
	}

	run, err := s.workflowStore.GetRun(r.Context(), runID)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err := s.requireTenantAccess(r, run.OrgID); err != nil {
		http.Error(w, "tenant access denied", http.StatusForbidden)
		return
	}

	var body chatSendRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	content := strings.TrimSpace(body.Content)
	if content == "" {
		http.Error(w, "content required", http.StatusBadRequest)
		return
	}

	memoryID := runMemoryID(run)
	if memoryID == "" {
		http.Error(w, "missing memory id", http.StatusBadRequest)
		return
	}

	client, err := chatRedisClient(s.memStore)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotImplemented)
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
		http.Error(w, "failed to encode chat message", http.StatusInternalServerError)
		return
	}

	key := chatHistoryKey(memoryID)
	if err := client.RPush(r.Context(), key, data).Err(); err != nil {
		http.Error(w, "failed to store chat message", http.StatusInternalServerError)
		return
	}
	if chatMaxHistory > 0 {
		_ = client.LTrim(r.Context(), key, -chatMaxHistory, -1).Err()
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(chatMessageFromEvent(runID, ev.ID, ev))
}

func runMemoryID(run *wf.WorkflowRun) string {
	if run == nil || run.ID == "" {
		return ""
	}
	if run.Input != nil {
		if raw, ok := run.Input["memory_id"]; ok {
			if s, ok := raw.(string); ok {
				if trimmed := strings.TrimSpace(s); trimmed != "" {
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

func chatRedisClient(store memory.Store) (redis.UniversalClient, error) {
	rs, ok := store.(*memory.RedisStore)
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

func normalizeChatRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "user":
		return "user"
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

var errChatStoreUnavailable = errors.New("chat history unavailable")
