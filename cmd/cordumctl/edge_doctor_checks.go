package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
)

var edgeDemoPolicyRequiredRules = []string{
	"claude-code.deny-secret-reads",
	"claude-code.deny-destructive-shell",
	"claude-code.require-approval-for-edits",
	"claude-code.require-approval-for-vcs-push",
	"claude-code.require-approval-for-network",
	"claude-code.allow-safe-build-test",
	"claude-code.deny-unknown-high-risk",
}

func edgeCheckGatewayReachable(ctx context.Context, env *edgeDoctorEnv) checkResult {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, env.base.gateway+"/readyz", nil)
	if err != nil {
		return checkResult{State: stateFail, Detail: "invalid gateway URL", Fix: "set --gateway or CORDUM_GATEWAY to the Gateway base URL"}
	}
	resp, err := env.base.httpClient.Do(req)
	if err != nil {
		return checkResult{State: stateFail, Detail: "GET /readyz failed: " + edgeDoctorNetworkError(err), Fix: "start Gateway or correct --gateway"}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return checkResult{State: stateFail, Detail: fmt.Sprintf("GET /readyz returned %d", resp.StatusCode), Fix: "check Gateway logs"}
	}
	return checkResult{State: stateOK, Detail: edgeSafeURL(env.base.gateway) + "/readyz 200"}
}

func edgeCheckGatewayAuthTenant(ctx context.Context, env *edgeDoctorEnv) checkResult {
	if strings.TrimSpace(env.base.apiKey) == "" {
		return checkResult{State: stateFail, Detail: "no API key configured", Fix: "export CORDUM_API_KEY or pass --api-key"}
	}
	var status gatewayStatusResponse
	code, err := edgeDoctorGET(ctx, env, "/api/v1/status", &status)
	if err != nil {
		return checkResult{State: stateFail, Detail: "GET /api/v1/status failed: " + edgeDoctorNetworkError(err), Fix: "check Gateway logs and network"}
	}
	switch code {
	case http.StatusOK:
		return checkResult{State: stateOK, Detail: "authenticated tenant " + env.base.tenant + "; gateway build " + status.Build.Version}
	case http.StatusUnauthorized:
		return checkResult{State: stateFail, Detail: "401 Unauthorized from /api/v1/status", Fix: "check CORDUM_API_KEY"}
	case http.StatusForbidden:
		return checkResult{State: stateFail, Detail: "403 Forbidden from /api/v1/status", Fix: "check X-Tenant-ID and API key permissions"}
	default:
		return checkResult{State: stateFail, Detail: fmt.Sprintf("/api/v1/status returned %d", code), Fix: "check Gateway logs"}
	}
}

func edgeCheckSafetyKernelViaGateway(ctx context.Context, env *edgeDoctorEnv) checkResult {
	if !edgeDoctorHasAPIKey(env) {
		return checkResult{State: stateSkip, Detail: "skipped — CORDUM_API_KEY is not configured"}
	}
	code, err := edgeDoctorGET(ctx, env, "/api/v1/policy/snapshots", nil)
	if err != nil {
		return checkResult{State: stateFail, Detail: "GET /api/v1/policy/snapshots failed: " + edgeDoctorNetworkError(err), Fix: "check Gateway-to-Safety Kernel connectivity"}
	}
	switch code {
	case http.StatusOK:
		return checkResult{State: stateOK, Detail: "Gateway reached Safety Kernel snapshots endpoint"}
	case http.StatusServiceUnavailable, http.StatusBadGateway:
		return checkResult{State: stateFail, Detail: fmt.Sprintf("/api/v1/policy/snapshots returned %d", code), Fix: "restart safety-kernel and verify SAFETY_KERNEL_ADDR"}
	case http.StatusUnauthorized, http.StatusForbidden:
		return checkResult{State: stateFail, Detail: fmt.Sprintf("/api/v1/policy/snapshots returned %d", code), Fix: "use an API key with policy read/operator permission"}
	default:
		return checkResult{State: stateFail, Detail: fmt.Sprintf("/api/v1/policy/snapshots returned %d", code), Fix: "check Gateway logs"}
	}
}

func edgeCheckSessionsAPI(ctx context.Context, env *edgeDoctorEnv) checkResult {
	if !edgeDoctorHasAPIKey(env) {
		return checkResult{State: stateSkip, Detail: "skipped — CORDUM_API_KEY is not configured"}
	}
	code, err := edgeDoctorGET(ctx, env, "/api/v1/edge/sessions?limit=1", nil)
	if err != nil {
		return checkResult{State: stateFail, Detail: "GET /api/v1/edge/sessions failed: " + edgeDoctorNetworkError(err), Fix: "check Gateway Edge routes"}
	}
	if code == http.StatusOK {
		return checkResult{State: stateOK, Detail: "/api/v1/edge/sessions?limit=1 200"}
	}
	return checkResult{State: stateFail, Detail: fmt.Sprintf("/api/v1/edge/sessions returned %d", code), Fix: "verify Edge P0 Gateway routes and tenant auth"}
}

func edgeCheckClaudeBinary(_ context.Context, env *edgeDoctorEnv) checkResult {
	return edgeCheckExecutable(env, "claude", env.claudePath, "claude", "install Claude Code or pass --claude-path")
}

func edgeCheckHookBinary(_ context.Context, env *edgeDoctorEnv) checkResult {
	hook := edgeDoctorCommandExecutable(env.hookCommand)
	return edgeCheckExecutable(env, "cordum-hook", hook, "cordum-hook", "build/install cordum-hook or pass --hook-command")
}

func edgeCheckAgentdBinary(_ context.Context, env *edgeDoctorEnv) checkResult {
	return edgeCheckExecutable(env, "cordum-agentd", env.agentdPath, "cordum-agentd", "build/install cordum-agentd or pass --agentd-path")
}

func edgeCheckGeneratedSettings(_ context.Context, env *edgeDoctorEnv) checkResult {
	path := strings.TrimSpace(env.settingsPath)
	if path == "" {
		return checkResult{State: stateWarn, Detail: "Claude settings path could not be resolved", Fix: "pass --settings-path or set CORDUM_EDGE_SETTINGS_PATH"}
	}
	data, err := env.readFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return checkResult{State: stateWarn, Detail: "settings.json not found at " + path, Fix: "run cordumctl edge claude --settings-output " + path}
		}
		return checkResult{State: stateFail, Detail: "read settings.json failed: " + edgeDoctorRedact(err.Error(), env.base.apiKey), Fix: "check file permissions"}
	}
	if err := validateEdgeDoctorSettings(data); err != nil {
		return checkResult{State: stateFail, Detail: "invalid generated settings: " + err.Error(), Fix: "regenerate with cordumctl edge claude --settings-output"}
	}
	return checkResult{State: stateOK, Detail: "Cordum command hooks present and no secrets/nonce persisted"}
}

func edgeCheckAgentdStatus(ctx context.Context, env *edgeDoctorEnv) checkResult {
	raw := strings.TrimSpace(env.agentdURL)
	if raw == "" {
		raw = defaultEdgeAgentdURL
	}
	host, err := edgeLoopbackHost(raw)
	if err != nil {
		return checkResult{State: stateFail, Detail: err.Error(), Fix: "use a loopback HTTP CORDUM_AGENTD_URL"}
	}
	if err := env.dialTCP(ctx, host); err != nil {
		return checkResult{State: stateFail, Detail: "local agentd not reachable at " + host + "; " + edgeModeImplication(env.policyMode), Fix: "start cordumctl edge claude or cordum-agentd"}
	}
	return checkResult{State: stateOK, Detail: "loopback listener reachable at " + host}
}

func edgeCheckDemoPolicy(ctx context.Context, env *edgeDoctorEnv) checkResult {
	if !edgeDoctorHasAPIKey(env) {
		return checkResult{State: stateSkip, Detail: "skipped — CORDUM_API_KEY is not configured"}
	}
	var body edgePolicyRulesResponse
	code, err := edgeDoctorGET(ctx, env, "/api/v1/policy/rules", &body)
	if err != nil {
		return checkResult{State: stateWarn, Detail: "could not verify Edge demo policy: " + edgeDoctorNetworkError(err), Fix: "load examples/cordum-edge-pack before demos"}
	}
	if code != http.StatusOK {
		return checkResult{State: stateWarn, Detail: fmt.Sprintf("/api/v1/policy/rules returned %d", code), Fix: "use an API key with policy read permission"}
	}
	missing := missingEdgeDemoRules(body.Items)
	if len(missing) > 0 {
		return checkResult{State: stateWarn, Detail: fmt.Sprintf("Edge demo policy missing %d required rule(s): %s", len(missing), strings.Join(missing, ",")), Fix: "cordumctl pack install ./examples/cordum-edge-pack"}
	}
	return checkResult{State: stateOK, Detail: "Edge demo policy fixture rules loaded"}
}

func edgeCheckDashboardReachable(ctx context.Context, env *edgeDoctorEnv) checkResult {
	if strings.TrimSpace(env.dashboardURL) == "" {
		return checkResult{State: stateSkip, Detail: "dashboard URL not configured"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(env.dashboardURL, "/")+"/", nil)
	if err != nil {
		return checkResult{State: stateWarn, Detail: "invalid dashboard URL", Fix: "set --dashboard-url or CORDUM_EDGE_DASHBOARD_URL"}
	}
	resp, err := env.base.httpClient.Do(req)
	if err != nil {
		return checkResult{State: stateWarn, Detail: "dashboard GET failed: " + edgeDoctorNetworkError(err), Fix: "start dashboard or set --dashboard-url"}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		return checkResult{State: stateOK, Detail: edgeSafeURL(env.dashboardURL) + " reachable"}
	}
	return checkResult{State: stateWarn, Detail: fmt.Sprintf("dashboard returned %d", resp.StatusCode), Fix: "check dashboard service"}
}

func edgeCheckPolicyMode(_ context.Context, env *edgeDoctorEnv) checkResult {
	switch edgePolicyModeOrDefault(env.policyMode) {
	case "observe":
		return checkResult{State: stateOK, Detail: "observe degrades open: actions may continue with degraded evidence"}
	case "enforce":
		return checkResult{State: stateOK, Detail: "enforce degrades closed for risky/unknown actions; fix failures before demos"}
	case "enterprise-strict":
		return checkResult{State: stateWarn, Detail: "enterprise-strict fails closed if Gateway, Safety Kernel, agentd, hook, or settings are unavailable", Fix: "deploy managed settings and supervised agentd bootstrap"}
	default:
		return checkResult{State: stateFail, Detail: "invalid policy mode " + env.policyMode, Fix: "use observe, enforce, or enterprise-strict"}
	}
}

func edgeDoctorGET(ctx context.Context, env *edgeDoctorEnv, path string, out interface{}) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, env.base.gateway+path, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("X-API-Key", env.base.apiKey)
	if strings.TrimSpace(env.base.tenant) != "" {
		req.Header.Set("X-Tenant-ID", env.base.tenant)
	}
	resp, err := env.base.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, fmt.Errorf("decode body: %w", err)
		}
	}
	return resp.StatusCode, nil
}

type edgePolicyRulesResponse struct {
	Items []struct {
		ID string `json:"id"`
	} `json:"items"`
}

func missingEdgeDemoRules(items []struct {
	ID string `json:"id"`
}) []string {
	present := map[string]bool{}
	for _, item := range items {
		present[strings.TrimSpace(item.ID)] = true
	}
	var missing []string
	for _, id := range edgeDemoPolicyRequiredRules {
		if !present[id] {
			missing = append(missing, id)
		}
	}
	return missing
}
