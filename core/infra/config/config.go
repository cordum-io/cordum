package config

import (
	"log/slog"
	"os"
	"strings"
)

const (
	defaultNATSURL       = "nats://localhost:4222"
	defaultRedisURL      = "redis://localhost:6379"
	defaultSafetyKernel  = "localhost:50051"
	defaultPoolConfig    = "config/pools.yaml"
	defaultTimeoutConfig = "config/timeouts.yaml"
	defaultSafetyPolicy  = "config/safety.yaml"
	defaultContextEngine = ":50070"
	envNATSURL           = "NATS_URL"
	envRedisURL          = "REDIS_URL"
	envSafetyKernelAddr  = "SAFETY_KERNEL_ADDR"
	envContextEngineAddr = "CONTEXT_ENGINE_ADDR"
	envPoolConfigPath    = "POOL_CONFIG_PATH"
	envTimeoutConfigPath = "TIMEOUT_CONFIG_PATH"
	envSafetyPolicyPath  = "SAFETY_POLICY_PATH"
	envOutputPolicy      = "OUTPUT_POLICY_ENABLED"
)

// Config holds runtime configuration for the control plane components.
type Config struct {
	NatsURL             string
	RedisURL            string
	SafetyKernelAddr    string
	ContextEngineAddr   string
	PoolConfigPath      string
	TimeoutConfigPath   string
	SafetyPolicyPath    string
	OutputPolicyEnabled bool
}

// Load returns configuration using environment variables with sane defaults.
func Load() *Config {
	natsURL := os.Getenv(envNATSURL)
	if natsURL == "" {
		natsURL = defaultNATSURL
	}

	redisURL := os.Getenv(envRedisURL)
	if redisURL == "" {
		redisURL = defaultRedisURL
	}

	safetyAddr := os.Getenv(envSafetyKernelAddr)
	if safetyAddr == "" {
		safetyAddr = defaultSafetyKernel
	}
	contextEngineAddr := os.Getenv(envContextEngineAddr)
	if contextEngineAddr == "" {
		contextEngineAddr = defaultContextEngine
	}

	poolCfg := os.Getenv(envPoolConfigPath)
	if poolCfg == "" {
		poolCfg = defaultPoolConfig
	}
	timeoutCfg := os.Getenv(envTimeoutConfigPath)
	if timeoutCfg == "" {
		timeoutCfg = defaultTimeoutConfig
	}
	safetyPolicy := os.Getenv(envSafetyPolicyPath)
	if safetyPolicy == "" {
		safetyPolicy = defaultSafetyPolicy
	}
	outputPolicyEnabled := strings.EqualFold(os.Getenv(envOutputPolicy), "true") || os.Getenv(envOutputPolicy) == "1"

	cfg := &Config{
		NatsURL:             natsURL,
		RedisURL:            redisURL,
		SafetyKernelAddr:    safetyAddr,
		ContextEngineAddr:   contextEngineAddr,
		PoolConfigPath:      poolCfg,
		TimeoutConfigPath:   timeoutCfg,
		SafetyPolicyPath:    safetyPolicy,
		OutputPolicyEnabled: outputPolicyEnabled,
	}

	// Warn when using localhost defaults — likely misconfiguration in non-dev deployments.
	for _, pair := range [][2]string{
		{envNATSURL, natsURL},
		{envRedisURL, redisURL},
		{envSafetyKernelAddr, safetyAddr},
	} {
		if os.Getenv(pair[0]) == "" && (strings.Contains(pair[1], "localhost") || strings.Contains(pair[1], "127.0.0.1")) {
			slog.Warn("using localhost default — set env var for production", "var", pair[0], "value", pair[1])
		}
	}

	return cfg
}
