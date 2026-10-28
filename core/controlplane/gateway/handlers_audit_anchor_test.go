package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
)

func adoptAnchorRequest(role, body string) *http.Request {
	return withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/audit/chain/retention-anchor", strings.NewReader(body)), &auth.AuthContext{
		Role:        role,
		Tenant:      "default",
		PrincipalID: role + "@example.com",
	})
}

// handTrimmedGateway seeds seq 1-6 in the default tenant and drops 1-3 the
// way the old docs advised (XTRIM MAXLEN), leaving no retention anchor.
func handTrimmedGateway(t *testing.T) (*server, *http.ServeMux) {
	t.Helper()
	s, _, _ := newTestGateway(t)
	enableTestAuth(s)
	seedChain(t, s, "default", 6)
	if err := s.redisClient().XTrimMaxLen(context.Background(), s.auditChainer.StreamKey("default"), 3).Err(); err != nil {
		t.Fatalf("xtrim: %v", err)
	}
	mux := http.NewServeMux()
	if err := s.registerRoutes(mux); err != nil {
		t.Fatalf("registerRoutes: %v", err)
	}
	return s, mux
}

func TestAdoptRetentionAnchorRoute(t *testing.T) {
	s, mux := handTrimmedGateway(t)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, adoptAnchorRequest("admin", `{"expected_oldest_seq":4}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var res audit.AdoptAnchorResult
	if err := json.NewDecoder(rec.Body).Decode(&res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Tenant != "default" || res.AdoptedSeq != 3 || res.PreviousAnchorState != "none" || res.EventSeq != 7 {
		t.Fatalf("result = %+v, want seq 3 adopted in default, recorded as seq 7", res)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/audit/verify?tenant=default", nil)))
	var ver audit.VerifyResult
	if err := json.NewDecoder(rec.Body).Decode(&ver); err != nil {
		t.Fatalf("decode verify: %v", err)
	}
	if ver.Status != audit.VerifyStatusPartial || ver.FirstSeq != 4 || ver.LastSeq != 7 {
		t.Fatalf("verify after adopting = %+v, want partial from seq 4 to the adoption event", ver)
	}

	entries, err := s.redisClient().XRevRangeN(context.Background(), s.auditChainer.StreamKey("default"), "+", "-", 1).Result()
	if err != nil || len(entries) != 1 {
		t.Fatalf("read chain tail: %v", err)
	}
	var ev audit.SIEMEvent
	if err := json.Unmarshal([]byte(entries[0].Values["event"].(string)), &ev); err != nil {
		t.Fatalf("decode tail: %v", err)
	}
	if ev.EventType != audit.EventRetentionAnchorAdopted || ev.Identity != "admin@example.com" || ev.Extra["adopted_seq"] != "3" {
		t.Fatalf("chain tail = %+v, want the adoption by admin@example.com", ev)
	}
}

func TestAdoptRetentionAnchorRoute_Errors(t *testing.T) {
	cases := map[string]struct {
		role, body string
		want       int
	}{
		"viewer":                 {"viewer", `{"expected_oldest_seq":4}`, http.StatusForbidden},
		"invalid body":           {"admin", `nope`, http.StatusBadRequest},
		"no expected seq":        {"admin", `{}`, http.StatusBadRequest},
		"other tenant":           {"admin", `{"tenant_id":"other","expected_oldest_seq":4}`, http.StatusForbidden},
		"wrong expected seq":     {"admin", `{"expected_oldest_seq":5}`, http.StatusConflict},
		"chain already anchored": {"admin", `{"expected_oldest_seq":4,"replace_invalid":true}`, http.StatusConflict},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, mux := handTrimmedGateway(t)
			if name == "chain already anchored" {
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, adoptAnchorRequest("admin", `{"expected_oldest_seq":4}`))
				if rec.Code != http.StatusOK {
					t.Fatalf("first adoption: %d %s", rec.Code, rec.Body.String())
				}
			}
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, adoptAnchorRequest(tc.role, tc.body))
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}

	t.Run("no chainer", func(t *testing.T) {
		s, mux := handTrimmedGateway(t)
		s.auditChainer = nil
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, adoptAnchorRequest("admin", `{"expected_oldest_seq":4}`))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503: %s", rec.Code, rec.Body.String())
		}
	})
}
