package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
)

func TestTenantIsolationLoginThrottle(t *testing.T) {
	for _, tc := range []struct {
		name       string
		perIPLimit string
		globalMax  string
		attempts   int
		distinctIP bool
	}{
		{name: "per-IP", perIPLimit: "5", globalMax: "50"},
		{name: "global", perIPLimit: "100", globalMax: "3", attempts: 3, distinctIP: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MAX_LOGIN_ATTEMPTS", tc.perIPLimit)
			t.Setenv("MAX_GLOBAL_LOGIN_ATTEMPTS", tc.globalMax)
			attempts := tc.attempts
			if !tc.distinctIP {
				attempts = maxLoginAttempts()
			}
			s, store := setupLoginIntegration(t)
			for tenant, password := range map[string]string{
				"tenant-a": "TenantAPass1!xy", "tenant-b": "TenantBPass2!xy",
			} {
				user := &auth.User{Username: "alice", Tenant: tenant, Role: "user"}
				if err := store.Create(context.Background(), user, password); err != nil {
					t.Fatalf("create %s: %v", tenant, err)
				}
			}
			ip := func(i int) string {
				if !tc.distinctIP {
					i = 1
				}
				return fmt.Sprintf("192.0.2.%d:4567", i)
			}
			for i := 1; i <= attempts; i++ {
				assertTenantLoginThrottleResponse(t, s, "tenant-a", "wrong-password", ip(i), http.StatusUnauthorized)
			}
			assertTenantLoginThrottleResponse(t, s, "tenant-a", "TenantAPass1!xy", ip(attempts+1), http.StatusUnauthorized)
			assertTenantLoginThrottleResponse(t, s, "tenant-b", "TenantBPass2!xy", ip(attempts+2), http.StatusOK)
		})
	}
}

func assertTenantLoginThrottleResponse(t *testing.T, s *server, tenant, password, ip string, status int) {
	t.Helper()
	body := fmt.Sprintf(`{"username":"alice","tenant":%q,"password":%q}`, tenant, password)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = ip
	rec := httptest.NewRecorder()
	s.handleLogin(rec, req)
	var response AuthLoginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode %s login response: %v", tenant, err)
	}
	if rec.Code != status {
		t.Errorf("%s status = %d, want %d", tenant, rec.Code, status)
	}
	if status == http.StatusOK {
		if !strings.HasPrefix(response.Token, "session-") || rec.Header().Get("Set-Cookie") == "" {
			t.Error("successful login must issue a session token and cookie")
		}
		return
	}
	requireStableErrorCode(t, rec, http.StatusUnauthorized, "AUTH_INVALID_CREDENTIALS")
	if response.Token != "" || len(rec.Header().Values("Set-Cookie")) != 0 {
		t.Error("denied login must issue neither a session token nor a cookie")
	}
}
