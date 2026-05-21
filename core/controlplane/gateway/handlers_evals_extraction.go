package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/evals/extraction"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
)

var (
	incidentExtractionTimeout     = 60 * time.Second
	incidentExtractionRateLimiter = newFixedWindowLimiter(6, time.Hour)
)

type createDatasetFromIncidentsRequest struct {
	Tenant      string   `json:"tenant,omitempty"`
	Since       string   `json:"since,omitempty"`
	Until       string   `json:"until,omitempty"`
	Topic       string   `json:"topic,omitempty"`
	RuleID      string   `json:"rule_id,omitempty"`
	Verdicts    []string `json:"verdicts,omitempty"`
	AgentID     string   `json:"agent_id,omitempty"`
	MaxEntries  int      `json:"max_entries,omitempty"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	DryRun      bool     `json:"dry_run,omitempty"`
}

type createDatasetFromIncidentsResponse struct {
	DatasetID        string   `json:"dataset_id,omitempty"`
	Name             string   `json:"name"`
	Version          int      `json:"version,omitempty"`
	EntryCount       int      `json:"entry_count"`
	DedupedCount     int      `json:"deduped_count,omitempty"`
	ScannedDecisions int      `json:"scanned_decisions"`
	Warnings         []string `json:"warnings,omitempty"`
}

func (s *server) handleCreateDatasetFromIncidents(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermGovernanceRead, "admin") {
		return
	}
	if !s.requirePermissionOrRole(w, r, auth.PermEvalsDatasetsWrite, "admin") {
		return
	}
	if s.decisionLogStore == nil || s.evalDatasetStore == nil || s.jobStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "incident extraction unavailable")
		return
	}

	tenant, err := s.resolveTenant(r, "")
	if err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}

	if !incidentExtractionRateLimiter.Allow(tenant) {
		writeErrorJSON(w, http.StatusTooManyRequests, "incident extraction rate limit exceeded")
		return
	}

	var body createDatasetFromIncidentsRequest
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodeEvalExtractionFailed, "invalid json: "+err.Error())
		return
	}

	req, err := decodeExtractionRequest(body)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodeEvalExtractionFailed, err.Error())
		return
	}
	req.Tenant = tenant

	if rawDryRun := strings.TrimSpace(r.URL.Query().Get("dry_run")); rawDryRun != "" {
		parsedDryRun, err := parseStrictBool(rawDryRun)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, errorCodeEvalExtractionFailed, "dry_run must be true or false")
			return
		}
		req.DryRun = parsedDryRun
	}

	extractor := extraction.New(extraction.ExtractionDeps{
		DecisionLog:  s.decisionLogStore,
		JobStore:     s.jobStore,
		EvalDatasets: s.evalDatasetStore,
		Now:          func() time.Time { return time.Now().UTC() },
	})
	if _, err := extractor.Validate(req); err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodeEvalExtractionFailed, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), incidentExtractionTimeout)
	defer cancel()

	result, err := extractor.Run(ctx, req)
	if err != nil {
		var timeoutErr *extraction.TimeoutError
		switch {
		case errors.As(err, &timeoutErr):
			w.WriteHeader(http.StatusGatewayTimeout)
			writeJSON(w, toCreateDatasetFromIncidentsResponse(timeoutErr.Result))
			return
		case errors.Is(err, extraction.ErrNoIncidents):
			writeJSONError(w, http.StatusNotFound, errorCodeEvalExtractionFailed, "no matching incidents found")
			return
		case errors.Is(err, store.ErrEvalDatasetVersionExists):
			writeJSONError(w, http.StatusConflict, errorCodeEvalDatasetVersionConflict, "eval dataset version already exists")
			return
		case errors.Is(err, context.DeadlineExceeded):
			w.WriteHeader(http.StatusGatewayTimeout)
			writeJSON(w, createDatasetFromIncidentsResponse{Warnings: []string{"extraction timed out"}})
			return
		default:
			writeInternalError(w, r, "extract incidents into eval dataset", err)
			return
		}
	}

	status := http.StatusCreated
	if req.DryRun {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	writeJSON(w, toCreateDatasetFromIncidentsResponse(result))
}

func decodeExtractionRequest(body createDatasetFromIncidentsRequest) (extraction.ExtractionRequest, error) {
	since, err := parseExtractionTimestamp(body.Since)
	if err != nil {
		return extraction.ExtractionRequest{}, fmt.Errorf("invalid since timestamp")
	}
	until, err := parseExtractionTimestamp(body.Until)
	if err != nil {
		return extraction.ExtractionRequest{}, fmt.Errorf("invalid until timestamp")
	}
	verdicts := make([]model.SafetyDecision, 0, len(body.Verdicts))
	for _, verdict := range body.Verdicts {
		parsed, err := model.ParseDecisionLogVerdict(verdict)
		if err != nil {
			return extraction.ExtractionRequest{}, fmt.Errorf("invalid verdict %q", verdict)
		}
		verdicts = append(verdicts, parsed)
	}
	return extraction.ExtractionRequest{
		Since:              since,
		Until:              until,
		TopicPattern:       body.Topic,
		RuleID:             body.RuleID,
		Verdicts:           verdicts,
		AgentID:            body.AgentID,
		MaxEntries:         body.MaxEntries,
		DatasetName:        body.Name,
		DatasetDescription: body.Description,
		DryRun:             body.DryRun,
	}, nil
}

func parseExtractionTimestamp(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	for _, format := range []string{time.RFC3339Nano, time.RFC3339} {
		if parsed, err := time.Parse(format, raw); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid timestamp")
}

func parseStrictBool(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("invalid bool")
	}
}

func toCreateDatasetFromIncidentsResponse(result extraction.ExtractionResult) createDatasetFromIncidentsResponse {
	return createDatasetFromIncidentsResponse{
		DatasetID:        result.DatasetID,
		Name:             result.Name,
		Version:          result.Version,
		EntryCount:       result.EntryCount,
		DedupedCount:     result.DedupedCount,
		ScannedDecisions: result.ScannedDecisions,
		Warnings:         result.Warnings,
	}
}

type fixedWindowLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	now    func() time.Time
	state  map[string]fixedWindowState
}

type fixedWindowState struct {
	windowStart time.Time
	count       int
}

func newFixedWindowLimiter(limit int, window time.Duration) *fixedWindowLimiter {
	return &fixedWindowLimiter{
		limit:  limit,
		window: window,
		now:    func() time.Time { return time.Now().UTC() },
		state:  make(map[string]fixedWindowState),
	}
}

func (l *fixedWindowLimiter) Allow(key string) bool {
	if l == nil || l.limit <= 0 || l.window <= 0 {
		return true
	}
	key = strings.TrimSpace(key)
	if key == "" {
		key = "default"
	}

	now := l.now().UTC()
	windowStart := now.Truncate(l.window)

	l.mu.Lock()
	defer l.mu.Unlock()

	entry := l.state[key]
	if entry.windowStart != windowStart {
		entry = fixedWindowState{windowStart: windowStart}
	}
	if entry.count >= l.limit {
		l.state[key] = entry
		return false
	}
	entry.count++
	l.state[key] = entry
	return true
}

func (l *fixedWindowLimiter) reset() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.state = make(map[string]fixedWindowState)
}
