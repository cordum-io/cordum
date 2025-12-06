package config

import "os"

const defaultNATSURL = "nats://localhost:4222"

// Config holds runtime configuration for the control plane components.
type Config struct {
	NatsURL string
}

// Load returns configuration using environment variables with sane defaults.
func Load() *Config {
	url := os.Getenv("NATS_URL")
	if url == "" {
		url = defaultNATSURL
	}

	return &Config{
		NatsURL: url,
	}
}
