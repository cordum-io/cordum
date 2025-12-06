package config

import "os"

const (
	defaultNATSURL      = "nats://localhost:4222"
	defaultRedisURL     = "redis://localhost:6379"
	defaultSafetyKernel = "localhost:50051"
	envNATSURL          = "NATS_URL"
	envRedisURL         = "REDIS_URL"
	envSafetyKernelAddr = "SAFETY_KERNEL_ADDR"
)

// Config holds runtime configuration for the control plane components.
type Config struct {
	NatsURL          string
	RedisURL         string
	SafetyKernelAddr string
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

	return &Config{
		NatsURL:          natsURL,
		RedisURL:         redisURL,
		SafetyKernelAddr: safetyAddr,
	}
}
