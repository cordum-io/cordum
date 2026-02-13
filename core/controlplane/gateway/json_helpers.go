package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/cordum/cordum/core/infra/logging"
)

const (
	defaultMaxJSONBodyBytes   int64 = 2 * 1024 * 1024
	envGatewayMaxJSONBodyBytes      = "GATEWAY_MAX_JSON_BODY_BYTES"
)

var errRequestBodyTooLarge = errors.New("request body too large")

func maxJSONBodyBytes() int64 {
	if raw := strings.TrimSpace(os.Getenv(envGatewayMaxJSONBodyBytes)); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 {
			return v
		}
	}
	return defaultMaxJSONBodyBytes
}

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

func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) error {
	if r == nil {
		return errors.New("request required")
	}
	limit := maxJSONBodyBytes()
	if limit > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, limit)
	}
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return errRequestBodyTooLarge
		}
		return err
	}
	return nil
}

func writeJSONDecodeError(w http.ResponseWriter, err error, invalidMsg string) {
	if errors.Is(err, errRequestBodyTooLarge) {
		writeErrorJSON(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}
	writeErrorJSON(w, http.StatusBadRequest, invalidMsg)
}

// maxBodyMiddleware enforces a body size limit on mutating requests to prevent
// large-body DoS. Multipart uploads are excluded since those routes manage
// their own limits (e.g. pack install).
func maxBodyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch:
		default:
			next.ServeHTTP(w, r)
			return
		}

		// Skip multipart uploads — they have per-route limits.
		ct := r.Header.Get("Content-Type")
		if strings.HasPrefix(ct, "multipart/") {
			next.ServeHTTP(w, r)
			return
		}

		limit := maxJSONBodyBytes()

		// Fast reject if Content-Length is declared and exceeds limit.
		if r.ContentLength > limit {
			writeErrorJSON(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}

		// Wrap body to enforce limit even when Content-Length is absent (chunked).
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}

		next.ServeHTTP(w, r)
	})
}
