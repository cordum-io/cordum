package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	edgecore "github.com/cordum/cordum/core/edge"
	agentdcore "github.com/cordum/cordum/core/edge/agentd"
	"github.com/cordum/cordum/core/edge/claude"
	"github.com/cordum/cordum/core/infra/logging"
	"github.com/prometheus/client_golang/prometheus"
)

type cliOptions struct {
	Args   []string
	Env    map[string]string
	Stderr io.Writer
	Run    func(context.Context, runConfig) error
}

type runConfig struct {
	Gateway    string
	TenantID   string
	SocketPath string
	FailClosed bool
	Env        map[string]string
}

func main() {
	logging.Init("cordum-agentd")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	code := runCLI(ctx, cliOptions{
		Args:   os.Args[1:],
		Env:    environMap(os.Environ()),
		Stderr: os.Stderr,
		Run:    defaultRun,
	})
	os.Exit(code)
}

func runCLI(ctx context.Context, opts cliOptions) int {
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	env := cloneEnv(opts.Env)
	if env == nil {
		env = environMap(os.Environ())
	}

	cfg := runConfig{
		Gateway:    envValue(env, "CORDUM_GATEWAY"),
		TenantID:   envValue(env, "CORDUM_TENANT_ID"),
		SocketPath: envValue(env, "CORDUM_AGENTD_SOCKET"),
		FailClosed: parseBoolEnv(envValue(env, "CORDUM_AGENTD_FAIL_CLOSED")),
		Env:        env,
	}

	fs := flag.NewFlagSet("cordum-agentd", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&cfg.Gateway, "gateway", cfg.Gateway, "Cordum Gateway base URL")
	fs.StringVar(&cfg.TenantID, "tenant", cfg.TenantID, "Cordum tenant ID")
	fs.StringVar(&cfg.SocketPath, "socket", cfg.SocketPath, "local cordum-agentd hook URL (P0 supports http loopback only)")
	fs.BoolVar(&cfg.FailClosed, "fail-closed", cfg.FailClosed, "fail closed when local governance cannot start")
	help := fs.Bool("help", false, "show help")
	if err := fs.Parse(opts.Args); err != nil {
		_, _ = fmt.Fprintf(opts.Stderr, "cordum-agentd: %s\n", redactForStderr(err.Error(), env))
		writeUsage(opts.Stderr)
		return 2
	}
	if *help {
		writeUsage(opts.Stderr)
		return 0
	}
	if opts.Run == nil {
		_, _ = fmt.Fprintln(opts.Stderr, "cordum-agentd: runner not configured")
		return 1
	}
	if err := opts.Run(ctx, cfg); err != nil {
		_, _ = fmt.Fprintf(opts.Stderr, "cordum-agentd: %s\n", redactForStderr(err.Error(), env))
		return 1
	}
	return 0
}

func defaultRun(ctx context.Context, cfg runConfig) error {
	opts, err := defaultRunOptions(cfg)
	if err != nil {
		return err
	}
	return agentdcore.Run(ctx, opts)
}

func defaultRunOptions(cfg runConfig) (agentdcore.RunOptions, error) {
	return defaultRunOptionsWithRecorder(cfg, nil)
}

func defaultRunOptionsWithRecorder(cfg runConfig, recorder edgecore.Recorder) (agentdcore.RunOptions, error) {
	env := cloneEnv(cfg.Env)
	if env == nil {
		env = environMap(os.Environ())
	}
	if cfg.Gateway != "" {
		env["CORDUM_GATEWAY"] = cfg.Gateway
	}
	if cfg.TenantID != "" {
		env["CORDUM_TENANT_ID"] = cfg.TenantID
	}
	if cfg.SocketPath != "" {
		env["CORDUM_AGENTD_SOCKET"] = cfg.SocketPath
	}
	if cfg.FailClosed {
		env["CORDUM_AGENTD_FAIL_CLOSED"] = "true"
	}
	loaded, err := agentdcore.LoadConfig(env)
	if err != nil {
		return agentdcore.RunOptions{}, err
	}
	meta := agentdcore.GatherLocalMetadata(agentdcore.LocalMetadataOptions{Env: env})
	nonce, err := agentdcore.ValidateExternalNonce(envValue(env, "CORDUM_AGENTD_NONCE"))
	if err != nil {
		return agentdcore.RunOptions{}, err
	}
	if recorder == nil {
		recorder = edgecore.NewPrometheusRecorder(prometheus.DefaultRegisterer)
	}
	// EDGE-071: wire the package-level claude redaction recorder so the
	// fail-closed events from redactHookBoundaryString reach the same
	// Prometheus registry as the rest of agentd's edge metrics.
	claude.SetRedactionRecorder(recorder)
	return agentdcore.RunOptions{
		Config:   loaded,
		Metadata: meta,
		Nonce:    nonce,
		Recorder: recorder,
	}, nil
}

func writeUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `usage: cordum-agentd [flags]

Runs the local Cordum Edge agent daemon for Claude hook sessions.

Flags:
  --gateway URL       Cordum Gateway base URL (or CORDUM_GATEWAY)
  --tenant ID         Cordum tenant ID (or CORDUM_TENANT_ID)
  --socket URL        Local http loopback hook URL (or CORDUM_AGENTD_SOCKET)
  --fail-closed       Exit non-zero when governance cannot start (or CORDUM_AGENTD_FAIL_CLOSED=true)
  --help              Show this help

Environment:
  CORDUM_GATEWAY, CORDUM_API_KEY, CORDUM_TENANT_ID, CORDUM_AGENTD_SOCKET,
  CORDUM_EDGE_POLICY_MODE, CORDUM_AGENTD_LOG_LEVEL, CORDUM_AGENTD_HOOK_TIMEOUT,
  CORDUM_EDGE_HEARTBEAT_TTL, CORDUM_AGENTD_FAIL_CLOSED, CORDUM_AGENTD_NONCE
`)
}

func environMap(values []string) map[string]string {
	out := make(map[string]string, len(values))
	for _, value := range values {
		k, v, ok := strings.Cut(value, "=")
		if !ok {
			continue
		}
		out[k] = v
	}
	return out
}

func cloneEnv(env map[string]string) map[string]string {
	if env == nil {
		return nil
	}
	out := make(map[string]string, len(env))
	for k, v := range env {
		out[k] = v
	}
	return out
}

func envValue(env map[string]string, key string) string {
	if env == nil {
		return ""
	}
	return strings.TrimSpace(env[key])
}

func parseBoolEnv(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func redactForStderr(message string, env map[string]string) string {
	redacted := message
	for key, value := range env {
		if strings.TrimSpace(value) == "" || !isSensitiveEnvKey(key) {
			continue
		}
		redacted = strings.ReplaceAll(redacted, value, "[REDACTED]")
	}
	return redacted
}

func isSensitiveEnvKey(key string) bool {
	k := strings.ToLower(key)
	for _, marker := range []string{"password", "passwd", "secret", "token", "nonce", "api_key", "apikey", "credential", "auth"} {
		if strings.Contains(k, marker) {
			return true
		}
	}
	return false
}
