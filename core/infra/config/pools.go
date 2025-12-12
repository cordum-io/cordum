package config

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// PoolsConfig describes the topic-to-pool mapping consumed by the scheduler.
type PoolsConfig struct {
	Topics map[string]string `yaml:"topics"`
}

// LoadPoolTopics reads a YAML file containing a "topics" map and returns it.
func LoadPoolTopics(path string) (map[string]string, error) {
	if path == "" {
		return nil, errors.New("pool config path is empty")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read pool config: %w", err)
	}

	var cfg PoolsConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse pool config: %w", err)
	}

	if len(cfg.Topics) == 0 {
		return nil, errors.New("pool config has no topics")
	}

	for topic, pool := range cfg.Topics {
		if topic == "" || pool == "" {
			return nil, fmt.Errorf("invalid topic mapping: %q -> %q", topic, pool)
		}
	}

	return cfg.Topics, nil
}
