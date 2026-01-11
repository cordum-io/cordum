package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/gorilla/websocket"
	"google.golang.org/grpc/metadata"
)

type apiKeyEntry struct {
	Key string `json:"key"`
}

type BasicAuthProvider struct {
	defaultTenant        string
	keys                 map[string]struct{}
	requireAPIKey        bool
	allowHeaderPrincipal bool
}

func newBasicAuthProvider(defaultTenant string) (*BasicAuthProvider, error) {
	keys, requireKey, err := loadBasicAPIKeys()
	if err != nil {
		return nil, err
	}
	if defaultTenant == "" {
		defaultTenant = "default"
	}
	return &BasicAuthProvider{
		defaultTenant:        defaultTenant,
		keys:                 keys,
		requireAPIKey:        requireKey,
		allowHeaderPrincipal: true,
	}, nil
}

func (b *BasicAuthProvider) AuthenticateHTTP(r *http.Request) (*AuthContext, error) {
	if r == nil {
		return nil, errors.New("request required")
	}
	key := normalizeAPIKey(r.Header.Get("X-API-Key"))
	if key == "" && websocket.IsWebSocketUpgrade(r) {
		key = normalizeAPIKey(apiKeyFromWebSocket(r))
	}
	return b.authenticate(key, headerValue(r, "X-Principal-Id"))
}

func (b *BasicAuthProvider) AuthenticateGRPC(ctx context.Context) (*AuthContext, error) {
	key := ""
	principalID := ""
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if raw := md.Get("x-api-key"); len(raw) > 0 {
			key = normalizeAPIKey(raw[0])
		}
		if key == "" {
			if raw := md.Get("api-key"); len(raw) > 0 {
				key = normalizeAPIKey(raw[0])
			}
		}
		if raw := md.Get("x-principal-id"); len(raw) > 0 {
			principalID = strings.TrimSpace(raw[0])
		}
	}
	return b.authenticate(key, principalID)
}

func (b *BasicAuthProvider) authenticate(key, principalID string) (*AuthContext, error) {
	if b == nil {
		return &AuthContext{}, nil
	}
	if key == "" {
		if b.requireAPIKey {
			return nil, errors.New("api key required")
		}
		return &AuthContext{Tenant: b.defaultTenant, PrincipalID: strings.TrimSpace(principalID)}, nil
	}
	if len(b.keys) > 0 {
		if _, ok := b.keys[key]; !ok {
			return nil, errors.New("invalid api key")
		}
	}
	return &AuthContext{
		APIKey:      key,
		Tenant:      b.defaultTenant,
		PrincipalID: strings.TrimSpace(principalID),
	}, nil
}

func (b *BasicAuthProvider) RequireRole(_ *http.Request, _ ...string) error {
	return nil
}

func (b *BasicAuthProvider) ResolveTenant(_ *http.Request, requested, fallback string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		if b.defaultTenant != "" {
			return b.defaultTenant, nil
		}
		return strings.TrimSpace(fallback), nil
	}
	if b.defaultTenant != "" && requested != b.defaultTenant {
		return "", errors.New("tenant access denied")
	}
	return requested, nil
}

func (b *BasicAuthProvider) RequireTenantAccess(_ *http.Request, tenant string) error {
	tenant = strings.TrimSpace(tenant)
	if tenant == "" || b == nil {
		return nil
	}
	if b.defaultTenant != "" && tenant != b.defaultTenant {
		return errors.New("tenant access denied")
	}
	return nil
}

func (b *BasicAuthProvider) ResolvePrincipal(r *http.Request, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested != "" {
		return requested, nil
	}
	if b.allowHeaderPrincipal {
		return headerValue(r, "X-Principal-Id"), nil
	}
	return "", nil
}

func loadBasicAPIKeys() (map[string]struct{}, bool, error) {
	keys := map[string]struct{}{}
	requireKey := false

	raw := strings.TrimSpace(os.Getenv("CORDUM_API_KEYS"))
	if raw != "" {
		entries, err := parseAPIKeys(raw)
		if err != nil {
			return nil, false, err
		}
		for _, entry := range entries {
			if entry.Key == "" {
				continue
			}
			keys[entry.Key] = struct{}{}
		}
		requireKey = true
	}

	single := normalizeAPIKey(os.Getenv("CORDUM_SUPER_SECRET_API_TOKEN"))
	if single == "" {
		single = normalizeAPIKey(os.Getenv("CORDUM_API_KEY"))
	}
	if single == "" {
		single = normalizeAPIKey(os.Getenv("API_KEY"))
	}
	if single != "" {
		keys[single] = struct{}{}
		requireKey = true
	}

	return keys, requireKey, nil
}

func parseAPIKeys(raw string) ([]apiKeyEntry, error) {
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
		if len(chunks) == 1 {
			entry.Key = strings.TrimSpace(chunks[0])
		} else {
			entry.Key = strings.TrimSpace(chunks[1])
		}
		if entry.Key != "" {
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

func headerValue(r *http.Request, name string) string {
	if r == nil {
		return ""
	}
	return strings.TrimSpace(r.Header.Get(name))
}

func roleAllowed(role string, allowed ...string) bool {
	role = normalizeRole(role)
	if role == "" {
		return false
	}
	for _, candidate := range allowed {
		if role == normalizeRole(candidate) {
			return true
		}
	}
	return false
}

func normalizeRole(role string) string {
	role = strings.ToLower(strings.TrimSpace(role))
	if role == "secops" || role == "operator" {
		return "admin"
	}
	return role
}
