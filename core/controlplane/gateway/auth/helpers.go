package auth

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// WSAuthSubprotocol is the WebSocket subprotocol prefix used during auth handshake.
const WSAuthSubprotocol = "cordum-api-key" // #nosec G101 -- subprotocol identifier, not a credential

// HeaderValue returns a trimmed HTTP header value.
func HeaderValue(r *http.Request, name string) string {
	if r == nil {
		return ""
	}
	return strings.TrimSpace(r.Header.Get(name))
}

// NormalizeRole normalizes a role string to lowercase and maps known aliases.
func NormalizeRole(role string) string {
	role = strings.ToLower(strings.TrimSpace(role))
	if role == "secops" || role == "operator" {
		return "admin"
	}
	return role
}

// NormalizeAPIKey cleans an API key string of surrounding whitespace and quotes.
func NormalizeAPIKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	// Common .env mistake: quoting values (e.g. "example-key").
	key = strings.Trim(key, "\"'")
	return strings.TrimSpace(key)
}

// BearerToken extracts a bearer token from an Authorization header value.
func BearerToken(header string) string {
	header = strings.TrimSpace(header)
	if header == "" {
		return ""
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 {
		return ""
	}
	if !strings.EqualFold(parts[0], "bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

// ParseAPIKeys parses API key entries from various formats (JSON array,
// JSON object, JSON wrapped, or comma-separated).
func ParseAPIKeys(raw string) ([]apiKeyEntry, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if strings.HasPrefix(raw, "[") {
		var entries []apiKeyEntry
		if err := json.Unmarshal([]byte(raw), &entries); err != nil {
			return nil, fmt.Errorf("parse CORDUM_API_KEYS: %w", err)
		}
		return entries, nil
	}
	if strings.HasPrefix(raw, "{") {
		entries := map[string]apiKeyEntry{}
		if err := json.Unmarshal([]byte(raw), &entries); err == nil {
			out := make([]apiKeyEntry, 0, len(entries))
			for key, entry := range entries {
				entry.Key = key
				out = append(out, entry)
			}
			return out, nil
		}
		var wrapped struct {
			Keys []apiKeyEntry `json:"keys"`
		}
		if err := json.Unmarshal([]byte(raw), &wrapped); err != nil {
			return nil, fmt.Errorf("parse CORDUM_API_KEYS: %w", err)
		}
		return wrapped.Keys, nil
	}
	parts := strings.Split(raw, ",")
	entries := make([]apiKeyEntry, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		chunks := strings.Split(part, ":")
		entry := apiKeyEntry{}
		switch len(chunks) {
		case 1:
			entry.Key = strings.TrimSpace(chunks[0])
		case 2:
			entry.Tenant = strings.TrimSpace(chunks[0])
			entry.Key = strings.TrimSpace(chunks[1])
		case 3:
			entry.Tenant = strings.TrimSpace(chunks[0])
			entry.Key = strings.TrimSpace(chunks[1])
			entry.Role = strings.TrimSpace(chunks[2])
		default:
			entry.Tenant = strings.TrimSpace(chunks[0])
			entry.Key = strings.TrimSpace(chunks[1])
			entry.Role = strings.TrimSpace(chunks[2])
			entry.PrincipalID = strings.TrimSpace(chunks[3])
		}
		if entry.Key != "" {
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

// MergeAPIKeyEntries merges parsed API key entries into a key metadata map.
func MergeAPIKeyEntries(keys map[string]apiKeyMeta, entries []apiKeyEntry) error {
	for _, entry := range entries {
		if entry.Key == "" {
			continue
		}
		meta := apiKeyMeta{
			Tenant:           strings.TrimSpace(entry.Tenant),
			Role:             NormalizeRole(entry.Role),
			PrincipalID:      strings.TrimSpace(entry.PrincipalID),
			AllowCrossTenant: entry.AllowCrossTenant,
		}
		if entry.ExpiresAt != "" {
			ts, err := time.Parse(time.RFC3339, strings.TrimSpace(entry.ExpiresAt))
			if err != nil {
				return fmt.Errorf("parse api key expiry: %w", err)
			}
			meta.ExpiresAt = ts
		}
		if meta.Role == "" {
			meta.Role = "admin"
		}
		keys[entry.Key] = meta
	}
	return nil
}

// APIKeyFromWebSocket extracts an API key from WebSocket subprotocols.
func APIKeyFromWebSocket(r *http.Request) string {
	if r == nil {
		return ""
	}
	protocols := websocket.Subprotocols(r)
	for i, protocol := range protocols {
		if strings.EqualFold(protocol, WSAuthSubprotocol) && i+1 < len(protocols) {
			return DecodeWSAPIKey(protocols[i+1])
		}
		prefix := strings.ToLower(WSAuthSubprotocol) + "."
		if strings.HasPrefix(strings.ToLower(protocol), prefix) {
			token := protocol[len(prefix):]
			return DecodeWSAPIKey(token)
		}
	}
	return ""
}

// DecodeWSAPIKey decodes a base64-encoded WebSocket API key token.
func DecodeWSAPIKey(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if decoded, err := base64.RawURLEncoding.DecodeString(raw); err == nil {
		return string(decoded)
	}
	if decoded, err := base64.StdEncoding.DecodeString(raw); err == nil {
		return string(decoded)
	}
	return raw
}

// ---------------------------------------------------------------------------
// Env helpers (copied from gateway_helpers.go to avoid circular imports)
// ---------------------------------------------------------------------------

func durationFromEnv(key string, fallback time.Duration) time.Duration {
	if raw := strings.TrimSpace(os.Getenv(key)); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			return d
		}
	}
	return fallback
}

func intFromEnv(key string, fallback int) int {
	if raw := strings.TrimSpace(os.Getenv(key)); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			return v
		}
	}
	return fallback
}
