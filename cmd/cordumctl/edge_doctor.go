package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultEdgeDoctorDeadline = 30 * time.Second
	defaultEdgeAgentdURL      = "http://127.0.0.1:8765/v1/edge/hooks/claude"
)

type edgeDoctorEnv struct {
	base                *doctorEnv
	policyMode          string
	claudePath          string
	hookCommand         string
	agentdPath          string
	agentdURL           string
	settingsPath        string
	managedSettingsPath string
	dashboardURL        string
	lookPath            func(string) (string, error)
	statFile            func(string) (os.FileInfo, error)
	readFile            func(string) ([]byte, error)
	dialTCP             func(context.Context, string) error
}

type edgeDoctorOptions struct {
	policyMode          string
	claudePath          string
	hookCommand         string
	agentdPath          string
	agentdURL           string
	settingsPath        string
	managedSettingsPath string
	dashboardURL        string
}

type edgeDoctorCheck struct {
	id    string
	label string
	run   func(context.Context, *edgeDoctorEnv) checkResult
}

type edgeDoctorJSONEnvelope struct {
	Checks     []checkResult  `json:"checks"`
	Summary    map[string]int `json:"summary"`
	ExitCode   int            `json:"exitCode"`
	PolicyMode string         `json:"policyMode"`
}

func runEdgeDoctorCmd(args []string, stdout, stderr io.Writer) int {
	stdout, stderr = edgeDoctorWriters(stdout, stderr)
	fs := newFlagSet("edge doctor")
	jsonOutput := fs.Bool("json", false, "emit machine-readable JSON diagnostics")
	timeoutSec := fs.Int("timeout", int(defaultEdgeDoctorDeadline/time.Second), "overall deadline in seconds")
	policyMode := fs.String("policy-mode", firstEnvDefault("enforce", "CORDUM_EDGE_POLICY_MODE"), "policy mode: observe, enforce, or enterprise-strict")
	claudePath := fs.String("claude-path", firstEnv("CLAUDE_PATH"), "Claude Code binary path")
	hookCommand := fs.String("hook-command", firstEnvDefault("cordum-hook", "CORDUM_HOOK_COMMAND"), "cordum-hook command/path from generated settings")
	agentdPath := fs.String("agentd-path", firstEnv("CORDUM_AGENTD_PATH"), "cordum-agentd binary path")
	agentdURL := fs.String("agentd-url", firstEnvDefault(defaultEdgeAgentdURL, "CORDUM_AGENTD_URL", "CORDUM_AGENTD_SOCKET"), "local cordum-agentd hook URL")
	settingsPath := fs.String("settings-path", firstEnvDefault(defaultClaudeSettingsPath(), "CORDUM_EDGE_SETTINGS_PATH", "CLAUDE_SETTINGS_PATH"), "Claude settings.json path to validate")
	managedSettingsPath := fs.String("managed-settings-path", firstEnv("CORDUM_EDGE_MANAGED_SETTINGS_PATH"), "managed-settings.json path for the managed_settings_compliance check (empty = skip)")
	dashboardURL := fs.String("dashboard-url", firstEnv("CORDUM_EDGE_DASHBOARD_URL", "CORDUM_DASHBOARD_URL"), "dashboard URL to probe")
	shadowCluster, shadowCI := registerEdgeDoctorShadowFlags(fs.FlagSet)
	fs.ParseArgs(args)

	if exit, handled := dispatchEdgeDoctorShadow(fs.FlagSet, shadowCluster, shadowCI, *jsonOutput, stdout, stderr); handled {
		return exit
	}

	env, err := buildEdgeDoctorEnv(fs, edgeDoctorOptions{
		policyMode:          *policyMode,
		claudePath:          *claudePath,
		hookCommand:         *hookCommand,
		agentdPath:          *agentdPath,
		agentdURL:           *agentdURL,
		settingsPath:        *settingsPath,
		managedSettingsPath: *managedSettingsPath,
		dashboardURL:        *dashboardURL,
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "cordumctl edge doctor: %s\n", edgeDoctorRedact(err.Error(), *fs.apiKey))
		return 2
	}

	deadline := time.Duration(*timeoutSec) * time.Second
	if deadline <= 0 {
		deadline = defaultEdgeDoctorDeadline
	}
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()

	results := runEdgeDoctorChecks(ctx, env, defaultEdgeDoctorChecks())
	exitCode := edgeDoctorExitCode(results)
	if *jsonOutput {
		emitEdgeDoctorJSONTo(stdout, results, env.policyMode, exitCode)
	} else {
		emitEdgeDoctorHumanTo(stdout, results, env.policyMode, exitCode)
	}
	return exitCode
}

// dispatchEdgeDoctorShadow returns (exitCode, true) when one of the
// preview flags is set; the standard doctor checks pipeline is skipped
// in that case because previews invoke the EDGE-143.1/.2/.3 detectors
// directly. When neither flag is set the second return value is false
// and runEdgeDoctorCmd falls through to its existing flow.
func dispatchEdgeDoctorShadow(fs *flag.FlagSet, shadowCluster, shadowCI *string, asJSON bool, stdout, stderr io.Writer) (int, bool) {
	var clusterSet, ciSet bool
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "shadow-cluster":
			clusterSet = true
		case "shadow-ci":
			ciSet = true
		}
	})
	if ciSet {
		return runShadowCIPreview(*shadowCI, asJSON, stdout, stderr), true
	}
	if clusterSet {
		return runShadowClusterPreview(*shadowCluster, asJSON, stdout, stderr), true
	}
	return 0, false
}

func edgeDoctorWriters(stdout, stderr io.Writer) (io.Writer, io.Writer) {
	if stdout == nil {
		stdout = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
	}
	return stdout, stderr
}

func buildEdgeDoctorEnv(fs *flagSet, opts edgeDoctorOptions) (*edgeDoctorEnv, error) {
	base, err := buildDoctorEnv(fs, true, "")
	if err != nil {
		return nil, err
	}
	return &edgeDoctorEnv{
		base:                base,
		policyMode:          strings.TrimSpace(opts.policyMode),
		claudePath:          strings.TrimSpace(opts.claudePath),
		hookCommand:         strings.TrimSpace(opts.hookCommand),
		agentdPath:          strings.TrimSpace(opts.agentdPath),
		agentdURL:           strings.TrimSpace(opts.agentdURL),
		settingsPath:        strings.TrimSpace(opts.settingsPath),
		managedSettingsPath: strings.TrimSpace(opts.managedSettingsPath),
		dashboardURL:        strings.TrimSpace(opts.dashboardURL),
		lookPath:            exec.LookPath,
		statFile:            os.Stat,
		readFile:            os.ReadFile,
		dialTCP:             defaultEdgeDoctorDialTCP,
	}, nil
}

func defaultEdgeDoctorChecks() []edgeDoctorCheck {
	return []edgeDoctorCheck{
		{id: "gateway_reachable", label: "Gateway reachable", run: edgeCheckGatewayReachable},
		{id: "gateway_auth_tenant", label: "Gateway auth + tenant", run: edgeCheckGatewayAuthTenant},
		{id: "safety_kernel_gateway", label: "Safety Kernel via Gateway", run: edgeCheckSafetyKernelViaGateway},
		{id: "edge_sessions_api", label: "Edge sessions API", run: edgeCheckSessionsAPI},
		{id: "claude_binary", label: "Claude binary", run: edgeCheckClaudeBinary},
		{id: "cordum_hook_binary", label: "cordum-hook binary", run: edgeCheckHookBinary},
		{id: "cordum_agentd_binary", label: "cordum-agentd binary", run: edgeCheckAgentdBinary},
		{id: "generated_settings", label: "Generated settings", run: edgeCheckGeneratedSettings},
		{id: "agentd_status", label: "Local agentd status", run: edgeCheckAgentdStatus},
		{id: "edge_demo_policy", label: "Edge demo policy", run: edgeCheckDemoPolicy},
		{id: "dashboard_reachable", label: "Dashboard reachable", run: edgeCheckDashboardReachable},
		{id: "policy_mode_implications", label: "Policy mode implications", run: edgeCheckPolicyMode},
		{id: "managed_settings_compliance", label: "Managed settings compliance", run: edgeCheckManagedSettings},
	}
}

func runEdgeDoctorChecks(ctx context.Context, env *edgeDoctorEnv, checks []edgeDoctorCheck) []checkResult {
	results := make([]checkResult, 0, len(checks))
	for _, c := range checks {
		if err := ctx.Err(); err != nil {
			results = append(results, checkResult{ID: c.id, Label: c.label, State: stateSkip, Detail: "overall deadline reached before this check ran"})
			continue
		}
		perCheckCtx, cancel := context.WithTimeout(ctx, defaultDoctorPerCheckTimeout)
		res := c.run(perCheckCtx, env)
		cancel()
		if res.ID == "" {
			res.ID = c.id
		}
		if res.Label == "" {
			res.Label = c.label
		}
		results = append(results, res)
	}
	return results
}

func emitEdgeDoctorJSONTo(w io.Writer, results []checkResult, policyMode string, exitCode int) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(edgeDoctorJSONEnvelope{
		Checks:     results,
		Summary:    summaryCounts(results),
		ExitCode:   exitCode,
		PolicyMode: edgePolicyModeOrDefault(policyMode),
	})
}

func emitEdgeDoctorHumanTo(w io.Writer, results []checkResult, policyMode string, exitCode int) {
	_, _ = fmt.Fprintf(w, "Cordum Edge doctor (policy_mode=%s, exit_code=%d)\n", edgePolicyModeOrDefault(policyMode), exitCode)
	emitHumanTo(w, false, results, true)
}

func edgeDoctorExitCode(results []checkResult) int {
	if countByState(results, stateFail) > 0 {
		return 1
	}
	if countByState(results, stateWarn) > 0 {
		return 2
	}
	return 0
}

func defaultClaudeSettingsPath() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".claude", "settings.json")
}
