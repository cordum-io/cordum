package config

import "os"

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
	envUsePlanner        = "USE_PLANNER"
	envPlannerTopic      = "PLANNER_TOPIC"
)

// Config holds runtime configuration for the control plane components.
type Config struct {
	NatsURL           string
	RedisURL          string
	SafetyKernelAddr  string
	ContextEngineAddr string
	PoolConfigPath    string
	TimeoutConfigPath string
	UsePlanner        bool
	PlannerTopic      string
	SafetyPolicyPath  string
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
	usePlanner := os.Getenv(envUsePlanner) == "true"
	plannerTopic := os.Getenv(envPlannerTopic)
	if plannerTopic == "" {
		plannerTopic = "job.workflow.plan"
	}

	return &Config{
		NatsURL:           natsURL,
		RedisURL:          redisURL,
		SafetyKernelAddr:  safetyAddr,
		ContextEngineAddr: contextEngineAddr,
		PoolConfigPath:    poolCfg,
		TimeoutConfigPath: timeoutCfg,
		UsePlanner:        usePlanner,
		PlannerTopic:      plannerTopic,
		SafetyPolicyPath:  safetyPolicy,
	}
}
