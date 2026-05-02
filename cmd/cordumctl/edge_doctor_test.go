package main

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/edge/claude"
)

func TestEdgeDoctorSuccessJSON(t *testing.T) {
	fx := newEdgeDoctorFixture(t)
	code, stdout, stderr := runEdgeDoctorForTest(t, fx.args("--json")...)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr, stdout)
	}
	if strings.Contains(stdout+stderr, fx.apiKey) {
		t.Fatalf("doctor leaked API key")
	}
	payload := decodeEdgeDoctorJSON(t, stdout)
	if payload.ExitCode != 0 || payload.Summary["fail"] != 0 || payload.Summary["warn"] != 0 {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	assertEdgeDoctorCheck(t, payload, "safety_kernel_gateway", stateOK)
	assertEdgeDoctorCheck(t, payload, "generated_settings", stateOK)
}

func TestEdgeDoctorMissingAPIKeyFails(t *testing.T) {
	fx := newEdgeDoctorFixture(t)
	args := fx.args("--json")
	args = removeFlag(args, "--api-key")
	code, stdout, _ := runEdgeDoctorForTest(t, args...)
	if code != 1 {
		t.Fatalf("exit=%d stdout=%s", code, stdout)
	}
	payload := decodeEdgeDoctorJSON(t, stdout)
	assertEdgeDoctorCheck(t, payload, "gateway_auth_tenant", stateFail)
	assertEdgeDoctorCheck(t, payload, "safety_kernel_gateway", stateSkip)
}

func TestEdgeDoctorGatewayUnavailableFails(t *testing.T) {
	fx := newEdgeDoctorFixture(t)
	args := fx.args("--json")
	args = replaceFlag(args, "--gateway", "http://127.0.0.1:1")
	code, stdout, _ := runEdgeDoctorForTest(t, args...)
	if code != 1 {
		t.Fatalf("exit=%d stdout=%s", code, stdout)
	}
	payload := decodeEdgeDoctorJSON(t, stdout)
	assertEdgeDoctorCheck(t, payload, "gateway_reachable", stateFail)
}

func TestEdgeDoctorMissingClaudeFailsWithoutRealClaude(t *testing.T) {
	fx := newEdgeDoctorFixture(t)
	args := replaceFlag(fx.args("--json"), "--claude-path", filepath.Join(t.TempDir(), "missing-claude"))
	code, stdout, _ := runEdgeDoctorForTest(t, args...)
	if code != 1 {
		t.Fatalf("exit=%d stdout=%s", code, stdout)
	}
	assertEdgeDoctorCheck(t, decodeEdgeDoctorJSON(t, stdout), "claude_binary", stateFail)
}

func TestEdgeDoctorMissingHookFails(t *testing.T) {
	fx := newEdgeDoctorFixture(t)
	args := replaceFlag(fx.args("--json"), "--hook-command", filepath.Join(t.TempDir(), "missing-hook"))
	code, stdout, _ := runEdgeDoctorForTest(t, args...)
	if code != 1 {
		t.Fatalf("exit=%d stdout=%s", code, stdout)
	}
	assertEdgeDoctorCheck(t, decodeEdgeDoctorJSON(t, stdout), "cordum_hook_binary", stateFail)
}

func TestEdgeDoctorEnterpriseStrictWarns(t *testing.T) {
	fx := newEdgeDoctorFixture(t)
	args := replaceFlag(fx.args("--json"), "--policy-mode", "enterprise-strict")
	code, stdout, _ := runEdgeDoctorForTest(t, args...)
	if code != 2 {
		t.Fatalf("exit=%d stdout=%s", code, stdout)
	}
	payload := decodeEdgeDoctorJSON(t, stdout)
	if payload.Summary["fail"] != 0 || payload.Summary["warn"] == 0 {
		t.Fatalf("unexpected summary: %+v", payload.Summary)
	}
	assertEdgeDoctorCheck(t, payload, "policy_mode_implications", stateWarn)
}

func TestEdgeDoctorHumanOutputIncludesRemediation(t *testing.T) {
	fx := newEdgeDoctorFixture(t)
	args := replaceFlag(fx.args(), "--hook-command", filepath.Join(t.TempDir(), "missing-hook"))
	code, stdout, _ := runEdgeDoctorForTest(t, args...)
	if code != 1 {
		t.Fatalf("exit=%d stdout=%s", code, stdout)
	}
	for _, want := range []string{"Cordum Edge doctor", "cordum_hook_binary", "build/install cordum-hook"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("human output missing %q:\n%s", want, stdout)
		}
	}
}

type edgeDoctorFixture struct {
	apiKey     string
	gatewayURL string
	dashboard  string
	agentdURL  string
	settings   string
	executable string
}

func newEdgeDoctorFixture(t *testing.T) edgeDoctorFixture {
	t.Helper()
	apiKey := "edge-doctor-secret-token"
	agentdURL := newEdgeDoctorAgentdURL(t)
	settings := writeEdgeDoctorSettings(t, agentdURL)
	return edgeDoctorFixture{
		apiKey:     apiKey,
		gatewayURL: newEdgeDoctorGateway(t, apiKey).URL,
		dashboard:  newEdgeDoctorDashboard(t).URL,
		agentdURL:  agentdURL,
		settings:   settings,
		executable: os.Args[0],
	}
}

func (f edgeDoctorFixture) args(extra ...string) []string {
	base := []string{
		"--gateway", f.gatewayURL,
		"--api-key", f.apiKey,
		"--tenant", "tenant-edge",
		"--policy-mode", "enforce",
		"--claude-path", f.executable,
		"--hook-command", `"` + f.executable + `"`,
		"--agentd-path", f.executable,
		"--agentd-url", f.agentdURL,
		"--settings-path", f.settings,
		"--dashboard-url", f.dashboard,
		"--timeout", "5",
	}
	return append(base, extra...)
}

func runEdgeDoctorForTest(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	t.Setenv("CORDUM_API_KEY", "")
	var stdout, stderr bytes.Buffer
	code := runEdgeDoctorCmd(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func newEdgeDoctorGateway(t *testing.T, apiKey string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/api/v1/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != apiKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"build":{"version":"test"},"nats":{"connected":true},"redis":{"ok":true},"workers":{"count":1}}`))
	})
	mux.HandleFunc("/api/v1/policy/snapshots", authJSONHandler(apiKey, `{"snapshots":["snap-edge"]}`))
	mux.HandleFunc("/api/v1/edge/sessions", authJSONHandler(apiKey, `{"items":[],"next_cursor":""}`))
	mux.HandleFunc("/api/v1/policy/rules", authJSONHandler(apiKey, edgeDoctorRulesJSON()))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func authJSONHandler(apiKey, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != apiKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

func newEdgeDoctorDashboard(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newEdgeDoctorAgentdURL(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	return "http://" + ln.Addr().String() + "/v1/edge/hooks/claude"
}

func writeEdgeDoctorSettings(t *testing.T, agentdURL string) string {
	t.Helper()
	data, err := claude.GenerateDevSettingsJSON(claude.DevSettingsOptions{
		SessionID: "sess-doctor", ExecutionID: "exec-doctor", AgentdURL: agentdURL,
		HookCommand: "cordum-hook", HookTimeout: time.Second, PolicyMode: "enforce",
		ApprovalWaitTimeout: time.Second, Platform: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func edgeDoctorRulesJSON() string {
	items := make([]string, 0, len(edgeDemoPolicyRequiredRules))
	for _, id := range edgeDemoPolicyRequiredRules {
		items = append(items, `{"id":"`+id+`"}`)
	}
	return `{"items":[` + strings.Join(items, ",") + `]}`
}

func decodeEdgeDoctorJSON(t *testing.T, raw string) edgeDoctorJSONEnvelope {
	t.Helper()
	var payload edgeDoctorJSONEnvelope
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("decode doctor JSON: %v raw=%s", err, raw)
	}
	return payload
}

func assertEdgeDoctorCheck(t *testing.T, payload edgeDoctorJSONEnvelope, id string, state checkState) {
	t.Helper()
	for _, check := range payload.Checks {
		if check.ID == id {
			if check.State != state {
				t.Fatalf("%s state=%s want %s check=%+v", id, check.State, state, check)
			}
			return
		}
	}
	t.Fatalf("check %s not found in %+v", id, payload.Checks)
}

func replaceFlag(args []string, flag, value string) []string {
	out := append([]string(nil), args...)
	for i := 0; i < len(out)-1; i++ {
		if out[i] == flag {
			out[i+1] = value
			return out
		}
	}
	return append(out, flag, value)
}

func removeFlag(args []string, flag string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == flag && i+1 < len(args) {
			i++
			continue
		}
		out = append(out, args[i])
	}
	return out
}
