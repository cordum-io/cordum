package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestErrorHelpersDoNotLeakInternals verifies that the safe error helpers
// return only generic messages to the client, regardless of the internal error.
func TestErrorHelpersDoNotLeakInternals(t *testing.T) {
	// Internal errors that must never appear in HTTP response bodies.
	sensitiveErrors := []error{
		errors.New("redis: connection refused at localhost:6379"),
		errors.New("dial tcp 10.0.0.5:6379: connection refused"),
		errors.New("nats: no responders for subject job.submit"),
		errors.New("config field cordum_admin_password is required"),
		errors.New("tenant-1 does not have role 'admin'"),
		errors.New("context deadline exceeded"),
		errors.New("redis.Nil"),
		errors.New("open /etc/cordum/safety.yaml: permission denied"),
		errors.New("grpc: the connection is closing"),
	}

	r := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)

	type helperCase struct {
		name   string
		call   func(w http.ResponseWriter, r *http.Request, err error)
		status int
		msg    string
	}
	helpers := []helperCase{
		{
			name: "writeInternalError",
			call: func(w http.ResponseWriter, r *http.Request, err error) {
				writeInternalError(w, r, "test op", err)
			},
			status: 500,
			msg:    "internal error",
		},
		{
			name: "writeBadGateway",
			call: func(w http.ResponseWriter, r *http.Request, err error) {
				writeBadGateway(w, r, "test op", err)
			},
			status: 502,
			msg:    "upstream service error",
		},
		{
			name: "writeServiceUnavailable",
			call: func(w http.ResponseWriter, r *http.Request, err error) {
				writeServiceUnavailable(w, r, "test op", err)
			},
			status: 503,
			msg:    "service unavailable",
		},
		{
			name: "writeForbidden",
			call: func(w http.ResponseWriter, r *http.Request, err error) {
				writeForbidden(w, r, err)
			},
			status: 403,
			msg:    "access denied",
		},
	}

	for _, h := range helpers {
		for _, sensitiveErr := range sensitiveErrors {
			t.Run(h.name+"/"+sensitiveErr.Error(), func(t *testing.T) {
				w := httptest.NewRecorder()
				h.call(w, r, sensitiveErr)

				if w.Code != h.status {
					t.Errorf("expected status %d, got %d", h.status, w.Code)
				}
				body := w.Body.String()
				// Response must contain only the generic message.
				var resp map[string]any
				if err := json.Unmarshal([]byte(body), &resp); err != nil {
					t.Fatalf("response is not valid JSON: %v", err)
				}
				if msg, _ := resp["error"].(string); msg != h.msg {
					t.Errorf("expected error message %q, got %q", h.msg, msg)
				}
				// Response must NOT contain the sensitive error details.
				if strings.Contains(body, sensitiveErr.Error()) {
					t.Errorf("response body contains sensitive error: %s", body)
				}
			})
		}
	}
}

// forbiddenBodyPatterns are strings that must never appear in HTTP error responses.
// They indicate internal details leaking to clients.
var forbiddenBodyPatterns = []string{
	"redis:",
	"nats:",
	"localhost:",
	":6379",
	":4222",
	":50051",
	"connection refused",
	"context deadline",
	"redis.Nil",
	"permission denied",
	"/etc/cordum/",
	"grpc:",
	"dial tcp",
}

// TestForbiddenResponseIsGeneric verifies that writeForbidden never exposes
// role names, tenant IDs, or internal error details.
func TestForbiddenResponseIsGeneric(t *testing.T) {
	roleErrors := []error{
		errors.New("user does not have role 'admin'"),
		errors.New("tenant default does not have access"),
		errors.New("principal user-123 is not authorized for tenant org-456"),
	}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
	for _, err := range roleErrors {
		t.Run(err.Error(), func(t *testing.T) {
			w := httptest.NewRecorder()
			writeForbidden(w, r, err)
			body := w.Body.String()
			if w.Code != http.StatusForbidden {
				t.Errorf("expected 403, got %d", w.Code)
			}
			var resp map[string]any
			if jsonErr := json.Unmarshal([]byte(body), &resp); jsonErr != nil {
				t.Fatalf("invalid JSON: %v", jsonErr)
			}
			if msg, _ := resp["error"].(string); msg != "access denied" {
				t.Errorf("expected generic 'access denied', got %q", msg)
			}
			// Must not contain tenant/role/principal info.
			for _, pattern := range []string{"admin", "tenant", "user-123", "org-456", "principal"} {
				if strings.Contains(body, pattern) {
					t.Errorf("response body leaks internal detail %q: %s", pattern, body)
				}
			}
		})
	}
}

// TestNoInternalErrorPatterns is a static analysis test that scans handler files
// for unsafe error patterns. This prevents regressions where someone adds
// writeErrorJSON(w, http.StatusInternalServerError, err.Error()) instead of
// using the safe helpers.
func TestNoInternalErrorPatterns(t *testing.T) {
	// Patterns that indicate unsafe error responses.
	unsafePatterns := []struct {
		name    string
		pattern *regexp.Regexp
	}{
		{
			name:    "500 with err.Error()",
			pattern: regexp.MustCompile(`writeErrorJSON\(w,\s*http\.StatusInternalServerError,\s*err\.Error\(\)`),
		},
		{
			name:    "502 with err.Error()",
			pattern: regexp.MustCompile(`writeErrorJSON\(w,\s*http\.StatusBadGateway,\s*err\.Error\(\)`),
		},
		{
			name:    "503 with err.Error()",
			pattern: regexp.MustCompile(`writeErrorJSON\(w,\s*http\.StatusServiceUnavailable,\s*err\.Error\(\)`),
		},
		{
			name:    "403 with err.Error()",
			pattern: regexp.MustCompile(`writeErrorJSON\(w,\s*http\.StatusForbidden,\s*err\.Error\(\)`),
		},
		{
			name:    "429 with err.Error()",
			pattern: regexp.MustCompile(`writeErrorJSON\(w,\s*http\.StatusTooManyRequests,\s*err\.Error\(\)`),
		},
	}

	handlerFiles, err := filepath.Glob("handlers_*.go")
	if err != nil {
		t.Fatalf("glob handlers: %v", err)
	}
	if len(handlerFiles) == 0 {
		// When tests run from the package directory, files are in cwd.
		// When run from repo root, need the full path.
		handlerFiles, _ = filepath.Glob(filepath.Join("core", "controlplane", "gateway", "handlers_*.go"))
	}
	if len(handlerFiles) == 0 {
		t.Skip("no handler files found")
	}

	for _, file := range handlerFiles {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Errorf("read %s: %v", file, err)
			continue
		}
		content := string(data)
		for _, up := range unsafePatterns {
			if locs := up.pattern.FindAllStringIndex(content, -1); len(locs) > 0 {
				for _, loc := range locs {
					// Find line number for better error reporting.
					line := strings.Count(content[:loc[0]], "\n") + 1
					t.Errorf("%s:%d: unsafe pattern %q found — use writeInternalError/writeForbidden/writeBadGateway/writeServiceUnavailable instead",
						file, line, up.name)
				}
			}
		}
	}
}
