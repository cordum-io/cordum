package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/cordum/cordum/core/telemetry"
)

func (s *server) handleGetTelemetryStatus(w http.ResponseWriter, r *http.Request) {
	if err := s.requireRole(r, "admin"); err != nil {
		writeForbidden(w, r, err)
		return
	}
	if s.telemetry == nil {
		writeJSON(w, map[string]any{"mode": "off"})
		return
	}
	status, err := s.telemetry.Status(r.Context())
	if err != nil {
		writeInternalError(w, r, "telemetry status", err)
		return
	}
	writeJSON(w, status)
}

func (s *server) handleGetTelemetryInspect(w http.ResponseWriter, r *http.Request) {
	if err := s.requireRole(r, "admin"); err != nil {
		writeForbidden(w, r, err)
		return
	}
	if s.telemetry == nil {
		writeJSON(w, nil)
		return
	}
	payload, err := s.telemetry.InspectPayload(r.Context())
	if err != nil {
		writeInternalError(w, r, "telemetry inspect", err)
		return
	}
	writeJSON(w, payload)
}

func (s *server) handleGetTelemetryExport(w http.ResponseWriter, r *http.Request) {
	if err := s.requireRole(r, "admin"); err != nil {
		writeForbidden(w, r, err)
		return
	}
	if s.telemetry == nil {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="cordum-telemetry.json"`)
		_, _ = w.Write([]byte("null"))
		return
	}
	payload, err := s.telemetry.ExportPayload(r.Context())
	if err != nil {
		writeInternalError(w, r, "telemetry export", err)
		return
	}
	if len(payload) == 0 {
		payload = []byte("null")
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="cordum-telemetry.json"`)
	_, _ = w.Write(payload)
}

func (s *server) handleGetTelemetryUsage(w http.ResponseWriter, r *http.Request) {
	if err := s.requireRole(r, "admin"); err != nil {
		writeForbidden(w, r, err)
		return
	}
	if s.telemetry == nil {
		writeJSON(w, map[string]any{})
		return
	}
	usage, err := s.telemetry.Usage(r.Context())
	if err != nil {
		writeInternalError(w, r, "telemetry usage", err)
		return
	}
	writeJSON(w, usage)
}

func (s *server) handleSetTelemetryConsent(w http.ResponseWriter, r *http.Request) {
	if err := s.requireRole(r, "admin"); err != nil {
		writeForbidden(w, r, err)
		return
	}
	if s.telemetry == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "telemetry not configured")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1024))
	if err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "invalid request")
		return
	}
	var req struct {
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	mode := telemetry.NormalizeMode(strings.TrimSpace(req.Mode))

	// Persist consent in Redis so it survives restarts
	if store := s.telemetryState; store != nil {
		if err := store.SetConsentMode(r.Context(), string(mode)); err != nil {
			writeInternalError(w, r, "persist telemetry consent", err)
			return
		}
	}

	// Apply immediately
	s.telemetry.SetMode(mode)

	writeJSON(w, map[string]any{
		"status": "updated",
		"mode":   string(mode),
	})
}
