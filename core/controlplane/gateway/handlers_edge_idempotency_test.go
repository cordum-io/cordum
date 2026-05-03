package gateway

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// EDGE-060 — edgeNormalizedRequestHash determinism + sensitivity.
// The hash is the wire contract that lets ReserveIdempotency detect
// "same key + same payload" replays vs "same key + DIFFERENT payload"
// conflicts. Two identical payloads MUST hash to the same hex; payloads
// differing in any field (including tenant_id baked into the normalized
// shape) MUST hash differently.
func TestEdgeNormalizedRequestHashIsDeterministicAndSensitive(t *testing.T) {
	cases := []struct {
		name string
		a    map[string]any
		b    map[string]any
		want string // "equal" or "different"
	}{
		{
			name: "identical payloads",
			a:    map[string]any{"tenant_id": "t1", "session_id": "s1", "principal_id": "p1"},
			b:    map[string]any{"tenant_id": "t1", "session_id": "s1", "principal_id": "p1"},
			want: "equal",
		},
		{
			name: "tenant differs",
			a:    map[string]any{"tenant_id": "t1", "session_id": "s1"},
			b:    map[string]any{"tenant_id": "t2", "session_id": "s1"},
			want: "different",
		},
		{
			name: "principal differs",
			a:    map[string]any{"tenant_id": "t1", "principal_id": "p1"},
			b:    map[string]any{"tenant_id": "t1", "principal_id": "p2"},
			want: "different",
		},
		{
			name: "single field added",
			a:    map[string]any{"tenant_id": "t1"},
			b:    map[string]any{"tenant_id": "t1", "extra": "v"},
			want: "different",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ha, err := edgeNormalizedRequestHash(tc.a)
			if err != nil {
				t.Fatalf("hash a: %v", err)
			}
			hb, err := edgeNormalizedRequestHash(tc.b)
			if err != nil {
				t.Fatalf("hash b: %v", err)
			}
			if ha == "" || len(ha) != 64 {
				t.Fatalf("hash a malformed: %q (want 64-char hex)", ha)
			}
			eq := ha == hb
			switch tc.want {
			case "equal":
				if !eq {
					t.Fatalf("hash a=%s b=%s, want equal", ha, hb)
				}
			case "different":
				if eq {
					t.Fatalf("hash a=%s b=%s, want different", ha, hb)
				}
			}
		})
	}
}

// EDGE-060 — prepareEdgeIdempotencyRequest extracts header, validates
// length, normalizes payload via edgeNormalizedRequestHash, and returns
// the populated EdgeIdempotencyRequest. Three return-tuple paths to pin:
//   - no key → idempotent=false, handled=false (caller proceeds non-idempotently).
//   - oversized key → handled=true (4xx already written; caller returns).
//   - valid key → idempotent=true, handled=false (req populated with hash).
func TestPrepareEdgeIdempotencyRequestReturnTriad(t *testing.T) {
	srv := &server{}

	t.Run("no key — non-idempotent path", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/edge/sessions", bytes.NewReader([]byte(`{}`)))
		req, idempotent, handled := srv.prepareEdgeIdempotencyRequest(w, r, "tenant-a", edgeSessionCreateEndpoint, map[string]any{})
		if idempotent || handled {
			t.Fatalf("idempotent=%v handled=%v, want both false", idempotent, handled)
		}
		if req.Key != "" {
			t.Fatalf("req.Key=%q, want empty", req.Key)
		}
	})

	t.Run("oversized key — handled with 4xx", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/edge/sessions", bytes.NewReader([]byte(`{}`)))
		// maxEdgeIdempotencyKeyBytes is 512; +1 trips the guard.
		r.Header.Set("Idempotency-Key", strings.Repeat("a", maxEdgeIdempotencyKeyBytes+1))
		_, idempotent, handled := srv.prepareEdgeIdempotencyRequest(w, r, "tenant-a", edgeSessionCreateEndpoint, map[string]any{})
		if idempotent {
			t.Fatalf("idempotent=true on oversized key, want false")
		}
		if !handled {
			t.Fatalf("handled=false, want true (response already written)")
		}
		if got := w.Code; got != http.StatusBadRequest {
			t.Fatalf("status=%d, want %d", got, http.StatusBadRequest)
		}
		if !strings.Contains(w.Body.String(), edgeErrCodeIdempotencyKeyTooLong) {
			t.Fatalf("body=%q does not include %q", w.Body.String(), edgeErrCodeIdempotencyKeyTooLong)
		}
	})

	t.Run("valid key — populated request", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/edge/sessions", bytes.NewReader([]byte(`{}`)))
		r.Header.Set("Idempotency-Key", "key-abc-123")
		req, idempotent, handled := srv.prepareEdgeIdempotencyRequest(w, r, "tenant-a", edgeSessionCreateEndpoint, map[string]any{"tenant_id": "tenant-a", "principal_id": "p1"})
		if !idempotent {
			t.Fatalf("idempotent=false, want true")
		}
		if handled {
			t.Fatalf("handled=true on valid key, want false")
		}
		if req.TenantID != "tenant-a" || req.Endpoint != edgeSessionCreateEndpoint || req.Key != "key-abc-123" {
			t.Fatalf("req=%#v, want tenant=tenant-a endpoint=%s key=key-abc-123", req, edgeSessionCreateEndpoint)
		}
		if req.RequestHash == "" || len(req.RequestHash) != 64 {
			t.Fatalf("req.RequestHash=%q, want 64-char hex", req.RequestHash)
		}
	})
}

// applyEdgeIdempotency wrapper unit tests are deferred to the per-endpoint
// integration tests in EDGE-060 steps 3-4 (handlers_edge_sessions_test.go +
// handlers_edge_approvals_test.go), which exercise reserve→writeFn→
// complete-or-release through a real miniredis-backed store. A pure-unit
// stand-in would require a full edgecore.Store fake (interface has many
// methods) for diminishing return — the integration tests pin behavior
// against the same Reserve/Complete/Release implementation production
// uses.
