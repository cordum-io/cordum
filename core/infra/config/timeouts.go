package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type TopicTimeout struct {
	TimeoutSeconds int64 `yaml:"timeout_seconds"`
	MaxRetries     int   `yaml:"max_retries"`
}

type WorkflowTimeout struct {
	ChildTimeoutSeconds int64 `yaml:"child_timeout_seconds"`
	TotalTimeoutSeconds int64 `yaml:"total_timeout_seconds"`
	MaxRetries          int   `yaml:"max_retries"`
}

type TimeoutsConfig struct {
	Workflows  map[string]WorkflowTimeout `yaml:"workflows"`
	Topics     map[string]TopicTimeout    `yaml:"topics"`
	Reconciler ReconcilerTimeout          `yaml:"reconciler"`
}

type ReconcilerTimeout struct {
	DispatchTimeoutSeconds int64 `yaml:"dispatch_timeout_seconds"`
	RunningTimeoutSeconds  int64 `yaml:"running_timeout_seconds"`
	ScanIntervalSeconds    int64 `yaml:"scan_interval_seconds"`
}

// LoadTimeouts loads a YAML timeouts file; returns defaults if missing.
func LoadTimeouts(path string) (*TimeoutsConfig, error) {
	if path == "" {
		return defaultTimeouts(), nil
	}
	// #nosec G304 -- timeouts config path is operator-provided.
	data, err := os.ReadFile(path)
	if err != nil {
		// Return defaults if file missing
		return defaultTimeouts(), fmt.Errorf("read timeouts config: %w", err)
	}
	var cfg TimeoutsConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return defaultTimeouts(), fmt.Errorf("parse timeouts config: %w", err)
	}
	// Fill empty with defaults
	def := defaultTimeouts()
	if cfg.Workflows == nil {
		cfg.Workflows = def.Workflows
	}
	if cfg.Topics == nil {
		cfg.Topics = def.Topics
	}
	if cfg.Reconciler == (ReconcilerTimeout{}) {
		cfg.Reconciler = def.Reconciler
	}
	return &cfg, nil
}

// ParseTimeouts parses timeouts config data from YAML/JSON bytes.
func ParseTimeouts(data []byte) (*TimeoutsConfig, error) {
	if len(data) == 0 {
		return defaultTimeouts(), nil
	}
	var cfg TimeoutsConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return defaultTimeouts(), fmt.Errorf("parse timeouts config: %w", err)
	}
	def := defaultTimeouts()
	if cfg.Workflows == nil {
		cfg.Workflows = def.Workflows
	}
	if cfg.Topics == nil {
		cfg.Topics = def.Topics
	}
	if cfg.Reconciler == (ReconcilerTimeout{}) {
		cfg.Reconciler = def.Reconciler
	}
	return &cfg, nil
}

func defaultTimeouts() *TimeoutsConfig {
	return &TimeoutsConfig{
		Workflows: map[string]WorkflowTimeout{},
		Topics:    map[string]TopicTimeout{},
		Reconciler: ReconcilerTimeout{
			DispatchTimeoutSeconds: 300,
			RunningTimeoutSeconds:  9000,
			ScanIntervalSeconds:    30,
		},
	}
}
