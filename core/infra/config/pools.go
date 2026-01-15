package config

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// PoolConfig describes a worker pool's declared capabilities.
type PoolConfig struct {
	Requires []string `yaml:"requires,omitempty"`
}

// PoolsConfig describes topic routing and pool capabilities.
type PoolsConfig struct {
	Topics map[string][]string   `yaml:"topics"`
	Pools  map[string]PoolConfig `yaml:"pools,omitempty"`
}

type rawPoolsConfig struct {
	Topics map[string]any        `yaml:"topics"`
	Pools  map[string]PoolConfig `yaml:"pools"`
}

// ParsePoolsConfig parses pools config data from YAML/JSON bytes.
func ParsePoolsConfig(data []byte) (*PoolsConfig, error) {
	if len(data) == 0 {
		return nil, nil
	}
	if err := validateConfigSchema("pools", poolsSchemaFile, data); err != nil {
		return nil, err
	}
	var raw rawPoolsConfig
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse pool config: %w", err)
	}
	topics, err := parseTopicPools(raw.Topics)
	if err != nil {
		return nil, fmt.Errorf("parse pool topics: %w", err)
	}
	if len(topics) == 0 {
		return nil, errors.New("pool config has no topics")
	}
	if raw.Pools == nil {
		raw.Pools = map[string]PoolConfig{}
	}
	return &PoolsConfig{
		Topics: topics,
		Pools:  raw.Pools,
	}, nil
}

// LoadPoolConfig reads a YAML file containing topics + pools.
func LoadPoolConfig(path string) (*PoolsConfig, error) {
	if path == "" {
		return nil, errors.New("pool config path is empty")
	}

	// #nosec G304 -- pool config path is operator-provided.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read pool config %s: %w", path, err)
	}
	if len(data) == 0 {
		return nil, errors.New("pool config is empty")
	}

	cfg, err := ParsePoolsConfig(data)
	if err != nil {
		return nil, fmt.Errorf("load pool config %s: %w", path, err)
	}
	return cfg, nil
}

// LoadPoolTopics returns a single-pool mapping for legacy callers.
func LoadPoolTopics(path string) (map[string]string, error) {
	cfg, err := LoadPoolConfig(path)
	if err != nil {
		return nil, fmt.Errorf("load pool topics: %w", err)
	}
	return cfg.TopicToPool(), nil
}

// TopicToPool picks the first pool for each topic mapping.
func (c *PoolsConfig) TopicToPool() map[string]string {
	out := make(map[string]string, len(c.Topics))
	for topic, pools := range c.Topics {
		if len(pools) == 0 {
			continue
		}
		out[topic] = pools[0]
	}
	return out
}

func parseTopicPools(raw map[string]any) (map[string][]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make(map[string][]string, len(raw))
	for topic, value := range raw {
		if topic == "" {
			return nil, fmt.Errorf("invalid topic mapping: empty topic")
		}
		switch v := value.(type) {
		case string:
			if v == "" {
				return nil, fmt.Errorf("invalid topic mapping: %q -> empty pool", topic)
			}
			out[topic] = []string{v}
		case []any:
			pools := make([]string, 0, len(v))
			for _, item := range v {
				pool, ok := item.(string)
				if !ok || pool == "" {
					return nil, fmt.Errorf("invalid pool list for topic %q", topic)
				}
				pools = append(pools, pool)
			}
			if len(pools) == 0 {
				return nil, fmt.Errorf("invalid topic mapping: %q -> empty pools", topic)
			}
			out[topic] = pools
		default:
			return nil, fmt.Errorf("invalid topic mapping for %q", topic)
		}
	}
	return out, nil
}
