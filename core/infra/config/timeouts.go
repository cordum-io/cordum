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
		return defaultTimeouts(), fmt.Errorf("read timeouts config %s: %w", path, err)
	}
	cfg, err := ParseTimeouts(data)
	if err != nil {
		return cfg, fmt.Errorf("load timeouts config %s: %w", path, err)
	}
	return cfg, nil
}

// ParseTimeouts parses timeouts config data from YAML/JSON bytes.
func ParseTimeouts(data []byte) (*TimeoutsConfig, error) {
	if len(data) == 0 {
		return defaultTimeouts(), nil
	}
	if err := validateConfigSchema("timeouts", timeoutsSchemaFile, data); err != nil {
		return defaultTimeouts(), err
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
	if err := cfg.Validate(); err != nil {
		return defaultTimeouts(), fmt.Errorf("validate timeouts config: %w", err)
	}
	return &cfg, nil
}

// Validate ensures timeouts are non-negative and internally consistent.
func (c *TimeoutsConfig) Validate() error {
	if c == nil {
		return nil
	}
	for name, timeout := range c.Topics {
		if timeout.TimeoutSeconds < 0 {
			return fmt.Errorf("topic %q timeout_seconds must be >= 0", name)
		}
		if timeout.MaxRetries < 0 {
			return fmt.Errorf("topic %q max_retries must be >= 0", name)
		}
	}
	for name, timeout := range c.Workflows {
		if timeout.ChildTimeoutSeconds < 0 {
			return fmt.Errorf("workflow %q child_timeout_seconds must be >= 0", name)
		}
		if timeout.TotalTimeoutSeconds < 0 {
			return fmt.Errorf("workflow %q total_timeout_seconds must be >= 0", name)
		}
		if timeout.MaxRetries < 0 {
			return fmt.Errorf("workflow %q max_retries must be >= 0", name)
		}
	}
	if c.Reconciler.DispatchTimeoutSeconds < 0 {
		return fmt.Errorf("reconciler dispatch_timeout_seconds must be >= 0")
	}
	if c.Reconciler.RunningTimeoutSeconds < 0 {
		return fmt.Errorf("reconciler running_timeout_seconds must be >= 0")
	}
	if c.Reconciler.ScanIntervalSeconds < 0 {
		return fmt.Errorf("reconciler scan_interval_seconds must be >= 0")
	}
	return nil
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
