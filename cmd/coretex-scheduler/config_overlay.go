package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"sort"
	"time"

	"errors"
	"github.com/redis/go-redis/v9"
	"github.com/yaront1111/coretex-os/core/configsvc"
	"github.com/yaront1111/coretex-os/core/controlplane/scheduler"
	"github.com/yaront1111/coretex-os/core/infra/config"
	"os"
)

type configSnapshot struct {
	Pools        *config.PoolsConfig
	PoolsHash    string
	Timeouts     *config.TimeoutsConfig
	TimeoutsHash string
}

func bootstrapConfig(ctx context.Context, svc *configsvc.Service, pools *config.PoolsConfig, timeouts *config.TimeoutsConfig) error {
	if svc == nil {
		return nil
	}
	doc, err := svc.Get(ctx, configsvc.ScopeSystem, "default")
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			return err
		}
		doc = &configsvc.Document{
			Scope:   configsvc.ScopeSystem,
			ScopeID: "default",
			Data:    map[string]any{},
		}
	}
	if doc.Data == nil {
		doc.Data = map[string]any{}
	}
	changed := false
	if pools != nil {
		if _, ok := doc.Data["pools"]; !ok {
			encoded, err := toMap(pools)
			if err != nil {
				return err
			}
			doc.Data["pools"] = encoded
			changed = true
		}
	}
	if timeouts != nil {
		if _, ok := doc.Data["timeouts"]; !ok {
			encoded, err := toMap(timeouts)
			if err != nil {
				return err
			}
			doc.Data["timeouts"] = encoded
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return svc.Set(ctx, doc)
}

func loadConfigSnapshot(ctx context.Context, svc *configsvc.Service, fallbackPools *config.PoolsConfig, fallbackTimeouts *config.TimeoutsConfig) (configSnapshot, error) {
	snap := configSnapshot{
		Pools:    fallbackPools,
		Timeouts: fallbackTimeouts,
	}
	if svc == nil {
		return snap, nil
	}
	doc, err := svc.Get(ctx, configsvc.ScopeSystem, "default")
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return snap, nil
		}
		return snap, err
	}
	if doc.Data == nil {
		return snap, nil
	}

	if raw, ok := doc.Data["pools"]; ok {
		pools, hash, err := parsePools(raw)
		if err != nil {
			log.Printf("scheduler: pools overlay ignored: %v", err)
		} else if pools != nil {
			snap.Pools = pools
			snap.PoolsHash = hash
		}
	}
	if raw, ok := doc.Data["timeouts"]; ok {
		timeouts, hash, err := parseTimeouts(raw)
		if err != nil {
			log.Printf("scheduler: timeouts overlay ignored: %v", err)
		} else if timeouts != nil {
			snap.Timeouts = timeouts
			snap.TimeoutsHash = hash
		}
	}
	return snap, nil
}

func watchConfigChanges(ctx context.Context, svc *configsvc.Service, fallbackPools *config.PoolsConfig, fallbackTimeouts *config.TimeoutsConfig, strategy *scheduler.LeastLoadedStrategy, reconciler *scheduler.Reconciler) {
	if svc == nil || strategy == nil || reconciler == nil {
		return
	}
	interval := 30 * time.Second
	if raw := os.Getenv("SCHEDULER_CONFIG_RELOAD_INTERVAL"); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			interval = parsed
		} else {
			log.Printf("scheduler: invalid SCHEDULER_CONFIG_RELOAD_INTERVAL=%q, using default %s", raw, interval)
		}
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var lastPoolsHash string
	var lastTimeoutsHash string
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			snap, err := loadConfigSnapshot(ctx, svc, fallbackPools, fallbackTimeouts)
			if err != nil {
				log.Printf("scheduler: config reload failed: %v", err)
				continue
			}
			if snap.Pools != nil && snap.PoolsHash != "" && snap.PoolsHash != lastPoolsHash {
				routing := buildRouting(snap.Pools)
				strategy.UpdateRouting(routing)
				lastPoolsHash = snap.PoolsHash
				log.Printf("scheduler: routing updated (%d topics)", len(routing.Topics))
			}
			if snap.Timeouts != nil && snap.TimeoutsHash != "" && snap.TimeoutsHash != lastTimeoutsHash {
				dispatch, running, _ := reconcilerTimeouts(snap.Timeouts)
				reconciler.UpdateTimeouts(dispatch, running)
				lastTimeoutsHash = snap.TimeoutsHash
				log.Printf("scheduler: reconciler timeouts updated (dispatch=%s, running=%s)", dispatch, running)
			}
		}
	}
}

func buildRouting(pools *config.PoolsConfig) scheduler.PoolRouting {
	routing := scheduler.PoolRouting{
		Topics: map[string][]string{},
		Pools:  map[string]scheduler.PoolProfile{},
	}
	if pools == nil {
		return routing
	}
	for topic, poolList := range pools.Topics {
		clone := make([]string, len(poolList))
		copy(clone, poolList)
		routing.Topics[topic] = clone
	}
	for name, pool := range pools.Pools {
		reqs := make([]string, len(pool.Requires))
		copy(reqs, pool.Requires)
		routing.Pools[name] = scheduler.PoolProfile{Requires: reqs}
	}
	return routing
}

func reconcilerTimeouts(cfg *config.TimeoutsConfig) (time.Duration, time.Duration, time.Duration) {
	if cfg == nil {
		cfg = &config.TimeoutsConfig{}
	}
	recCfg := cfg.Reconciler
	dispatchTimeout := time.Duration(recCfg.DispatchTimeoutSeconds) * time.Second
	if dispatchTimeout == 0 {
		dispatchTimeout = 2 * time.Minute
	}
	runningTimeout := time.Duration(recCfg.RunningTimeoutSeconds) * time.Second
	if runningTimeout == 0 {
		runningTimeout = 5 * time.Minute
	}
	scanInterval := time.Duration(recCfg.ScanIntervalSeconds) * time.Second
	if scanInterval == 0 {
		scanInterval = 30 * time.Second
	}
	return dispatchTimeout, runningTimeout, scanInterval
}

func parsePools(raw any) (*config.PoolsConfig, string, error) {
	payload, err := json.Marshal(raw)
	if err != nil {
		return nil, "", err
	}
	cfg, err := config.ParsePoolsConfig(payload)
	if err != nil {
		return nil, "", err
	}
	hash, err := hashAny(raw)
	if err != nil {
		return nil, "", err
	}
	return cfg, hash, nil
}

func parseTimeouts(raw any) (*config.TimeoutsConfig, string, error) {
	payload, err := json.Marshal(raw)
	if err != nil {
		return nil, "", err
	}
	cfg, err := config.ParseTimeouts(payload)
	if err != nil {
		return nil, "", err
	}
	hash, err := hashAny(raw)
	if err != nil {
		return nil, "", err
	}
	return cfg, hash, nil
}

func toMap(value any) (map[string]any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func hashAny(value any) (string, error) {
	data, err := canonicalJSON(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func canonicalJSON(value any) ([]byte, error) {
	buf := &bytes.Buffer{}
	if err := appendCanonical(buf, value); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func appendCanonical(buf *bytes.Buffer, value any) error {
	switch v := value.(type) {
	case nil:
		buf.WriteString("null")
		return nil
	case map[string]any:
		return appendCanonicalMap(buf, v)
	case []any:
		return appendCanonicalSlice(buf, v)
	default:
		encoded, err := json.Marshal(v)
		if err != nil {
			return err
		}
		buf.Write(encoded)
		return nil
	}
}

func appendCanonicalMap(buf *bytes.Buffer, m map[string]any) error {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	buf.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		keyBytes, _ := json.Marshal(k)
		buf.Write(keyBytes)
		buf.WriteByte(':')
		if err := appendCanonical(buf, m[k]); err != nil {
			return err
		}
	}
	buf.WriteByte('}')
	return nil
}

func appendCanonicalSlice(buf *bytes.Buffer, items []any) error {
	buf.WriteByte('[')
	for i, item := range items {
		if i > 0 {
			buf.WriteByte(',')
		}
		if err := appendCanonical(buf, item); err != nil {
			return err
		}
	}
	buf.WriteByte(']')
	return nil
}
