package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
	"sort"
	"sync"
	"time"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/infra/config"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
	"gopkg.in/yaml.v3"
)

type configSnapshot struct {
	Pools        *config.PoolsConfig
	PoolsHash    string
	Timeouts     *config.TimeoutsConfig
	TimeoutsHash string
}

type configChangeSubscriber interface {
	ReplaceSubscription(prev *nats.Subscription, subject, queue string, handler func(*pb.BusPacket) error) (*nats.Subscription, error)
	AddReconnectHandler(handler func(*nats.Conn))
	AddDisconnectHandler(handler func(*nats.Conn, error))
}

func registerSchedulerConfigNotifications(natsBus configChangeSubscriber, notifyCh chan struct{}) {
	if natsBus == nil || notifyCh == nil {
		return
	}

	callback := func(_ *pb.BusPacket) (err error) {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("scheduler: config subscription panic",
					"panic", r,
					"stack", string(debug.Stack()),
				)
				err = nil
			}
		}()
		select {
		case notifyCh <- struct{}{}:
		default: // coalesce rapid notifications
		}
		return nil
	}

	var (
		subMu     sync.Mutex
		configSub *nats.Subscription
	)
	subscribe := func() error {
		subMu.Lock()
		defer subMu.Unlock()
		sub, err := natsBus.ReplaceSubscription(configSub, capsdk.SubjectConfigChanged, "", callback)
		if err != nil {
			return err
		}
		configSub = sub
		return nil
	}

	natsBus.AddDisconnectHandler(func(_ *nats.Conn, err error) {
		slog.Error("scheduler: NATS disconnected, relying on poll", "error", err)
	})
	natsBus.AddReconnectHandler(func(_ *nats.Conn) {
		slog.Error("scheduler: NATS reconnected, re-subscribing", "subject", capsdk.SubjectConfigChanged)
		if err := subscribe(); err != nil {
			slog.Error("scheduler: failed to re-subscribe to config change notifications", "subject", capsdk.SubjectConfigChanged, "error", err)
			return
		}
		slog.Info("scheduler: re-subscribed to config change notifications", "subject", capsdk.SubjectConfigChanged)
	})

	if err := subscribe(); err != nil {
		slog.Warn("scheduler: failed to subscribe to config change notifications, relying on poll", "error", err)
		return
	}
	slog.Info("scheduler: subscribed to config change notifications", "subject", capsdk.SubjectConfigChanged)
}

func bootstrapConfig(ctx context.Context, svc *configsvc.Service, pools *config.PoolsConfig, timeouts *config.TimeoutsConfig) error {
	if svc == nil {
		return nil
	}
	doc, err := svc.Get(ctx, configsvc.ScopeSystem, "default")
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			return fmt.Errorf("bootstrap config: %w", err)
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
		encoded, err := toMap(pools)
		if err != nil {
			return fmt.Errorf("bootstrap config: %w", err)
		}
		fileHash, err := hashAny(encoded)
		if err != nil {
			return fmt.Errorf("bootstrap config: %w", err)
		}
		existingPools := doc.Data["pools"]
		storedFileHash, _ := doc.Data["_poolsFileHash"].(string)
		if existingPools == nil {
			// First-time bootstrap: write file config
			doc.Data["pools"] = encoded
			doc.Data["_poolsFileHash"] = fileHash
			changed = true
			slog.Info("pool config bootstrapped from file")
		} else if storedFileHash == "" {
			// Legacy data without hash tracking: record hash but don't overwrite
			doc.Data["_poolsFileHash"] = fileHash
			changed = true
		} else if storedFileHash == fileHash {
			// File unchanged — pack overlays may have been applied, don't touch
		} else {
			// File itself changed on disk — merge file base with existing overlays
			// by deep-merging file pools into existing (file is base, overlays on top)
			if existingMap, ok := existingPools.(map[string]any); ok {
				for k, v := range encoded {
					if _, exists := existingMap[k]; !exists {
						existingMap[k] = v
					} else if fileMap, ok := v.(map[string]any); ok {
						if existMap, ok := existingMap[k].(map[string]any); ok {
							for fk, fv := range fileMap {
								if _, has := existMap[fk]; !has {
									existMap[fk] = fv
								}
							}
						}
					}
				}
				doc.Data["pools"] = existingMap
			} else {
				doc.Data["pools"] = encoded
			}
			doc.Data["_poolsFileHash"] = fileHash
			changed = true
			slog.Info("pool config file changed — merged with existing overlays")
		}
	}
	if timeouts != nil {
		encoded, err := toMap(timeouts)
		if err != nil {
			return fmt.Errorf("bootstrap config: %w", err)
		}
		fileHash, err := hashAny(encoded)
		if err != nil {
			return fmt.Errorf("bootstrap config: %w", err)
		}
		if _, ok := doc.Data["timeouts"]; !ok {
			doc.Data["timeouts"] = encoded
			doc.Data["_timeoutsFileHash"] = fileHash
			changed = true
		} else {
			storedHash, _ := doc.Data["_timeoutsFileHash"].(string)
			if storedHash != fileHash {
				doc.Data["timeouts"] = encoded
				doc.Data["_timeoutsFileHash"] = fileHash
				changed = true
				slog.Info("timeouts config updated in Redis (hash changed)")
			}
		}
	}
	if !changed {
		return nil
	}
	err = svc.Set(ctx, doc)
	if errors.Is(err, configsvc.ErrRevisionConflict) {
		slog.Warn("bootstrap config: revision conflict, will retry on next cycle")
		return nil
	}
	return err
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
			slog.Warn("pools overlay ignored", "error", err)
		} else if pools != nil {
			snap.Pools = pools
			snap.PoolsHash = hash
		}
	}
	if raw, ok := doc.Data["timeouts"]; ok {
		timeouts, hash, err := parseTimeouts(raw)
		if err != nil {
			slog.Warn("timeouts overlay ignored", "error", err)
		} else if timeouts != nil {
			snap.Timeouts = timeouts
			snap.TimeoutsHash = hash
		}
	}
	return snap, nil
}

func watchConfigChanges(ctx context.Context, svc *configsvc.Service, fallbackPools *config.PoolsConfig, fallbackTimeouts *config.TimeoutsConfig, strategy *scheduler.LeastLoadedStrategy, reconciler *scheduler.Reconciler, natsBus *bus.NatsBus, engine *scheduler.Engine) {
	if svc == nil || strategy == nil || reconciler == nil {
		return
	}
	interval := 30 * time.Second
	if raw := os.Getenv("SCHEDULER_CONFIG_RELOAD_INTERVAL"); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			interval = parsed
		} else {
			slog.Warn("invalid SCHEDULER_CONFIG_RELOAD_INTERVAL, using default", "value", raw, "default", interval) // #nosec -- value is config input for diagnostics.
		}
	}

	// Subscribe to sys.config.changed (broadcast, empty queue group) for
	// immediate reload when any gateway writes config. The 30s poll remains
	// as a fallback in case the notification is missed.
	notifyCh := make(chan struct{}, 1)
	if natsBus != nil {
		registerSchedulerConfigNotifications(natsBus, notifyCh)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var lastPoolsHash string
	var lastTimeoutsHash string

	reload := func(trigger string) {
		snap, err := loadConfigSnapshot(ctx, svc, fallbackPools, fallbackTimeouts)
		if err != nil {
			slog.Error("config reload failed", "trigger", trigger, "error", err)
			return
		}
		if snap.Pools != nil && snap.PoolsHash != "" && snap.PoolsHash != lastPoolsHash {
			routing := buildRouting(snap.Pools)
			strategy.UpdateRouting(routing)
			lastPoolsHash = snap.PoolsHash
			slog.Info("routing updated", "topics", len(routing.Topics), "trigger", trigger)
		}
		if snap.Timeouts != nil && snap.TimeoutsHash != "" && snap.TimeoutsHash != lastTimeoutsHash {
			dispatch, running, _ := reconcilerTimeouts(snap.Timeouts)
			reconciler.UpdateTimeouts(dispatch, running)
			lastTimeoutsHash = snap.TimeoutsHash
			slog.Info("reconciler timeouts updated", "dispatch", dispatch, "running", running, "trigger", trigger)
		}
		// Reload fail modes from config if present
		if engine != nil {
			reloadFailModes(ctx, svc, engine, trigger)
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reload("poll")
		case <-notifyCh:
			slog.Info("scheduler: config change notification received, reloading")
			reload("notification")
		}
	}
}

// reloadFailModes reads scheduler settings from the system config and updates
// the engine's atomic flags. Called on every config reload cycle. Uses the
// Engine's With* methods which do atomic stores — safe for concurrent access.
func reloadFailModes(ctx context.Context, svc *configsvc.Service, engine *scheduler.Engine, trigger string) {
	if svc == nil {
		return
	}
	doc, err := svc.Get(ctx, configsvc.ScopeSystem, "default")
	if err != nil {
		return
	}
	schedulerCfg, ok := doc.Data["scheduler"].(map[string]any)
	if !ok {
		return
	}
	if mode, ok := schedulerCfg["input_fail_mode"].(string); ok {
		engine.WithInputFailMode(mode)
		slog.Info("scheduler input fail mode updated", "mode", mode, "trigger", trigger)
	}
	if mode, ok := schedulerCfg["output_fail_mode"].(string); ok {
		engine.WithAsyncFailMode(mode)
		slog.Info("scheduler output fail mode updated", "mode", mode, "trigger", trigger)
	}
	if enabled, ok := schedulerCfg["output_policy_enabled"].(bool); ok {
		engine.WithOutputSafetyEnabled(enabled)
		slog.Info("scheduler output policy enabled updated", "enabled", enabled, "trigger", trigger)
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
	// Build set of inactive pools to exclude from routing.
	inactivePools := make(map[string]bool)
	for name, pool := range pools.Pools {
		s := pool.EffectiveStatus()
		if s == config.PoolStatusDraining || s == config.PoolStatusInactive {
			slog.Debug("buildRouting: skipping pool", "pool", name, "status", s)
			inactivePools[name] = true
			continue
		}
		reqs := make([]string, len(pool.Requires))
		copy(reqs, pool.Requires)
		routing.Pools[name] = scheduler.PoolProfile{Requires: reqs}
	}
	for topic, poolList := range pools.Topics {
		var active []string
		for _, p := range poolList {
			if !inactivePools[p] {
				active = append(active, p)
			}
		}
		if len(active) > 0 {
			routing.Topics[topic] = active
		}
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
	normalized := normalizePoolsOverlay(raw)
	payload, err := json.Marshal(normalized)
	if err != nil {
		return nil, "", err
	}
	cfg, err := config.ParsePoolsConfig(payload)
	if err != nil {
		return nil, "", err
	}
	hash, err := hashAny(normalized)
	if err != nil {
		return nil, "", err
	}
	return cfg, hash, nil
}

func parseTimeouts(raw any) (*config.TimeoutsConfig, string, error) {
	normalized := normalizeTimeoutsOverlay(raw)
	payload, err := json.Marshal(normalized)
	if err != nil {
		return nil, "", err
	}
	cfg, err := config.ParseTimeouts(payload)
	if err != nil {
		return nil, "", err
	}
	hash, err := hashAny(normalized)
	if err != nil {
		return nil, "", err
	}
	return cfg, hash, nil
}

func toMap(value any) (map[string]any, error) {
	data, err := yaml.Marshal(value)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	if err := yaml.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func normalizePoolsOverlay(raw any) any {
	rawMap, ok := raw.(map[string]any)
	if !ok || rawMap == nil {
		return raw
	}
	out := map[string]any{}
	if val, ok := rawMap["topics"]; ok {
		out["topics"] = val
	} else if val, ok := rawMap["Topics"]; ok {
		out["topics"] = val
	}
	if val, ok := rawMap["pools"]; ok {
		out["pools"] = val
	} else if val, ok := rawMap["Pools"]; ok {
		out["pools"] = val
	}
	return out
}

func normalizeTimeoutsOverlay(raw any) any {
	rawMap, ok := raw.(map[string]any)
	if !ok || rawMap == nil {
		return raw
	}
	out := map[string]any{}
	if val, ok := rawMap["topics"]; ok {
		out["topics"] = val
	} else if val, ok := rawMap["Topics"]; ok {
		out["topics"] = normalizeTopicTimeouts(val)
	}
	if val, ok := rawMap["workflows"]; ok {
		out["workflows"] = val
	} else if val, ok := rawMap["Workflows"]; ok {
		out["workflows"] = normalizeWorkflowTimeouts(val)
	}
	if val, ok := rawMap["reconciler"]; ok {
		out["reconciler"] = val
	} else if val, ok := rawMap["Reconciler"]; ok {
		out["reconciler"] = normalizeReconcilerTimeouts(val)
	}
	return out
}

func normalizeTopicTimeouts(raw any) any {
	rawMap, ok := raw.(map[string]any)
	if !ok || rawMap == nil {
		return raw
	}
	out := map[string]any{}
	for name, value := range rawMap {
		item, ok := value.(map[string]any)
		if !ok || item == nil {
			out[name] = value
			continue
		}
		normalized := map[string]any{}
		if v, ok := item["timeout_seconds"]; ok {
			normalized["timeout_seconds"] = v
		} else if v, ok := item["TimeoutSeconds"]; ok {
			normalized["timeout_seconds"] = v
		}
		if v, ok := item["max_retries"]; ok {
			normalized["max_retries"] = v
		} else if v, ok := item["MaxRetries"]; ok {
			normalized["max_retries"] = v
		}
		if len(normalized) == 0 {
			out[name] = value
		} else {
			out[name] = normalized
		}
	}
	return out
}

func normalizeWorkflowTimeouts(raw any) any {
	rawMap, ok := raw.(map[string]any)
	if !ok || rawMap == nil {
		return raw
	}
	out := map[string]any{}
	for name, value := range rawMap {
		item, ok := value.(map[string]any)
		if !ok || item == nil {
			out[name] = value
			continue
		}
		normalized := map[string]any{}
		if v, ok := item["child_timeout_seconds"]; ok {
			normalized["child_timeout_seconds"] = v
		} else if v, ok := item["ChildTimeoutSeconds"]; ok {
			normalized["child_timeout_seconds"] = v
		}
		if v, ok := item["total_timeout_seconds"]; ok {
			normalized["total_timeout_seconds"] = v
		} else if v, ok := item["TotalTimeoutSeconds"]; ok {
			normalized["total_timeout_seconds"] = v
		}
		if v, ok := item["max_retries"]; ok {
			normalized["max_retries"] = v
		} else if v, ok := item["MaxRetries"]; ok {
			normalized["max_retries"] = v
		}
		if len(normalized) == 0 {
			out[name] = value
		} else {
			out[name] = normalized
		}
	}
	return out
}

func normalizeReconcilerTimeouts(raw any) any {
	rawMap, ok := raw.(map[string]any)
	if !ok || rawMap == nil {
		return raw
	}
	out := map[string]any{}
	if v, ok := rawMap["dispatch_timeout_seconds"]; ok {
		out["dispatch_timeout_seconds"] = v
	} else if v, ok := rawMap["DispatchTimeoutSeconds"]; ok {
		out["dispatch_timeout_seconds"] = v
	}
	if v, ok := rawMap["running_timeout_seconds"]; ok {
		out["running_timeout_seconds"] = v
	} else if v, ok := rawMap["RunningTimeoutSeconds"]; ok {
		out["running_timeout_seconds"] = v
	}
	if v, ok := rawMap["scan_interval_seconds"]; ok {
		out["scan_interval_seconds"] = v
	} else if v, ok := rawMap["ScanIntervalSeconds"]; ok {
		out["scan_interval_seconds"] = v
	}
	return out
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
			return fmt.Errorf("canonical encode: %w", err)
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
			return fmt.Errorf("canonical encode: %w", err)
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
			return fmt.Errorf("canonical encode: %w", err)
		}
	}
	buf.WriteByte(']')
	return nil
}
