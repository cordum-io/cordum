package gateway

import (
	"encoding/json"
	"net/http"

	"github.com/cordum/cordum/core/infra/logging"
)

// writeJSON encodes v as JSON into w, logging any encoding error.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		logging.Warn("api-gateway", "json encode failed", "error", err)
	}
}

// writeErrorJSON writes a structured JSON error response with the given HTTP status.
func writeErrorJSON(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	resp := map[string]any{"error": message, "status": status}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		logging.Warn("api-gateway", "json encode error response failed", "error", err)
	}
}
