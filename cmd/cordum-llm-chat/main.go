// Command cordum-llm-chat is the scaffold for the self-hosted Cordum LLM
// Chat Assistant service. Phase 1 of epic-ac495830 delivers the process
// boot — logger, buildinfo, env parsing, OpenAI-compat provider wiring,
// and /healthz + /readyz. MCP client, session store, and /api/v1/chat
// handlers land in follow-up tasks.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cordum/cordum/core/infra/buildinfo"
	"github.com/cordum/cordum/core/infra/logging"
	"github.com/cordum/cordum/core/llmchat"
	"github.com/redis/go-redis/v9"
)

const (
	defaultHTTPAddr            = ":8091"
	defaultProvider            = "openai"
	defaultBaseURL             = "http://qwen-inference:8000/v1"
	defaultModel               = "qwen3-coder"
	defaultToolTemperature     = 0.3
	defaultToolTopP            = 0.9
	defaultSummaryTemperature  = 0.7
	defaultSummaryTopP         = 0.8
	defaultMaxToolCallsPerTurn = 12
	defaultMaxWallClockPerTurn = 60 * time.Second
	defaultMaxAssistantBytes   = 32768
	readyzProbeTimeout         = 2 * time.Second
	shutdownGrace              = 10 * time.Second
)

// runtimeConfig is the fully-resolved, validated boot configuration.
// Kept separate from llmchat.ProviderConfig so transport + Redis wiring
// stays in the process binary, not leaked into the reusable provider
// package.
type runtimeConfig struct {
	HTTPAddr     string
	TLSCertFile  string
	TLSKeyFile   string
	RedisURL     string
	Provider     llmchat.ProviderConfig
	Budget       llmchat.BudgetConfig
	CordumAPIKey string
	GatewayURL   string
	NATSURL      string
}

func main() {
	logging.Init("llm-chat-server")
	buildinfo.Log("cordum-llm-chat")

	cfg, err := loadConfigFromEnv(os.Getenv)
	if err != nil {
		slog.Error("cordum-llm-chat: config load failed, refusing to start", "error", err)
		os.Exit(1)
	}

	provider, err := llmchat.ResolveProvider(cfg.Provider)
	if err != nil {
		slog.Error("cordum-llm-chat: provider resolve failed, refusing to start", "error", err)
		os.Exit(1)
	}

	redisClient, err := openRedis(cfg.RedisURL)
	if err != nil {
		slog.Error("cordum-llm-chat: redis connect failed, refusing to start", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := redisClient.Close(); err != nil {
			slog.Warn("cordum-llm-chat: redis close failed", "error", err)
		}
	}()

	handlers := llmchat.NewHandlers(provider, redisClient, readyzProbeTimeout)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", handlers.Healthz)
	mux.HandleFunc("/readyz", handlers.Readyz)

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		slog.Info("cordum-llm-chat listening",
			"addr", cfg.HTTPAddr,
			"tls", cfg.TLSCertFile != "",
			"provider", cfg.Provider.Kind,
			"base_url", cfg.Provider.BaseURL,
			"model", cfg.Provider.Model,
		)
		var err error
		if cfg.TLSCertFile != "" && cfg.TLSKeyFile != "" {
			err = srv.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile)
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	select {
	case <-ctx.Done():
		slog.Info("cordum-llm-chat: shutdown signal received")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			slog.Error("cordum-llm-chat: graceful shutdown failed", "error", err)
			os.Exit(1)
		}
		slog.Info("cordum-llm-chat: shutdown complete")
	case err := <-serveErr:
		if err != nil {
			slog.Error("cordum-llm-chat: http server failed", "error", err)
			os.Exit(1)
		}
	}
}

// loadConfigFromEnv resolves every boot env var into a validated
// runtimeConfig. Fails closed on missing required values and on any
// numeric parse error — operators should see a crisp error rather than
// a silent default that masks a typo.
func loadConfigFromEnv(getenv func(string) string) (runtimeConfig, error) {
	cfg := runtimeConfig{
		HTTPAddr:     envOrDefault(getenv, "CORDUM_LLM_CHAT_ADDR", defaultHTTPAddr),
		TLSCertFile:  strings.TrimSpace(getenv("CORDUM_LLM_CHAT_TLS_CERT_FILE")),
		TLSKeyFile:   strings.TrimSpace(getenv("CORDUM_LLM_CHAT_TLS_KEY_FILE")),
		RedisURL:     strings.TrimSpace(getenv("REDIS_URL")),
		CordumAPIKey: strings.TrimSpace(getenv("CORDUM_API_KEY")),
		GatewayURL:   strings.TrimSpace(getenv("CORDUM_GATEWAY_URL")),
		NATSURL:      strings.TrimSpace(getenv("NATS_URL")),
	}

	if cfg.RedisURL == "" {
		return runtimeConfig{}, fmt.Errorf("REDIS_URL is required")
	}
	if (cfg.TLSCertFile == "") != (cfg.TLSKeyFile == "") {
		return runtimeConfig{}, fmt.Errorf(
			"CORDUM_LLM_CHAT_TLS_CERT_FILE and CORDUM_LLM_CHAT_TLS_KEY_FILE must be set together",
		)
	}

	providerKind := strings.TrimSpace(getenv("LLMCHAT_PROVIDER"))
	if providerKind == "" {
		providerKind = defaultProvider
	}
	baseURL := strings.TrimSpace(getenv("LLMCHAT_BASE_URL"))
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	model := strings.TrimSpace(getenv("LLMCHAT_MODEL"))
	if model == "" {
		model = defaultModel
	}

	toolTemp, err := envFloatOrDefault(getenv, "LLMCHAT_TOOL_TEMPERATURE", defaultToolTemperature)
	if err != nil {
		return runtimeConfig{}, err
	}
	toolTopP, err := envFloatOrDefault(getenv, "LLMCHAT_TOOL_TOP_P", defaultToolTopP)
	if err != nil {
		return runtimeConfig{}, err
	}
	summaryTemp, err := envFloatOrDefault(getenv, "LLMCHAT_SUMMARY_TEMPERATURE", defaultSummaryTemperature)
	if err != nil {
		return runtimeConfig{}, err
	}
	summaryTopP, err := envFloatOrDefault(getenv, "LLMCHAT_SUMMARY_TOP_P", defaultSummaryTopP)
	if err != nil {
		return runtimeConfig{}, err
	}

	maxToolCalls, err := envIntOrDefault(getenv, "LLMCHAT_MAX_TOOL_CALLS_PER_TURN", defaultMaxToolCallsPerTurn)
	if err != nil {
		return runtimeConfig{}, err
	}
	maxWallClock, err := envDurationOrDefault(getenv, "LLMCHAT_MAX_WALL_CLOCK_PER_TURN", defaultMaxWallClockPerTurn)
	if err != nil {
		return runtimeConfig{}, err
	}
	maxAssistantBytes, err := envIntOrDefault(getenv, "LLMCHAT_MAX_ASSISTANT_BYTES", defaultMaxAssistantBytes)
	if err != nil {
		return runtimeConfig{}, err
	}

	cfg.Provider = llmchat.ProviderConfig{
		Kind:               providerKind,
		BaseURL:            baseURL,
		Model:              model,
		APIKey:             strings.TrimSpace(getenv("LLMCHAT_API_KEY")),
		ToolTemperature:    toolTemp,
		ToolTopP:           toolTopP,
		SummaryTemperature: summaryTemp,
		SummaryTopP:        summaryTopP,
	}
	cfg.Budget = llmchat.BudgetConfig{
		MaxToolCallsPerTurn: maxToolCalls,
		MaxWallClockPerTurn: maxWallClock,
		MaxAssistantBytes:   maxAssistantBytes,
	}
	return cfg, nil
}

func openRedis(redisURL string) (*redis.Client, error) {
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse REDIS_URL: %w", err)
	}
	client := redis.NewClient(options)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return client, nil
}

func envOrDefault(getenv func(string) string, key, fallback string) string {
	if val := strings.TrimSpace(getenv(key)); val != "" {
		return val
	}
	return fallback
}

func envFloatOrDefault(getenv func(string) string, key string, fallback float64) (float64, error) {
	raw := strings.TrimSpace(getenv(key))
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s=%q: %w", key, raw, err)
	}
	return v, nil
}

func envIntOrDefault(getenv func(string) string, key string, fallback int) (int, error) {
	raw := strings.TrimSpace(getenv(key))
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("parse %s=%q: %w", key, raw, err)
	}
	return v, nil
}

func envDurationOrDefault(getenv func(string) string, key string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(getenv(key))
	if raw == "" {
		return fallback, nil
	}
	v, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("parse %s=%q: %w", key, raw, err)
	}
	return v, nil
}
