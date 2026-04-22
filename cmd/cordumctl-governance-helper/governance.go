package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/encoding/protowire"
)

const (
	governanceDefaultRedisURL      = "redis://localhost:6379"
	governanceDefaultNATSURL       = "nats://localhost:4222"
	governanceAuditSubject         = "sys.audit.export"
	governanceTailQueue            = "cordumctl-governance-tail"
	governanceScanBatchSize        = int64(250)
	governanceJobRequestPrefix     = "job:req:"
	governanceJobMetaPrefix        = "job:meta:"
	governanceDecisionPrefix       = "gov:dec:"
	governanceDecisionJSONField    = "json"
	governanceDefaultDecisionTTL   = 30 * 24 * time.Hour
	governanceDecisionTTLSeconds   = "CORDUM_DECISION_LOG_TTL_SECONDS"
	governanceMetaSafetyDecision   = "safety_decision"
	governanceMetaSafetyReason     = "safety_reason"
	governanceMetaSafetyRuleID     = "safety_rule_id"
	governanceMetaSafetySnapshot   = "safety_snapshot"
	governanceMetaSafetyChecked    = "safety_checked_at"
	governanceMetaSafetyConst      = "safety_constraints"
	governanceMetaApprovalStatus   = "approval_status"
	governanceMetaApprovalDecision = "approval_decision"
	governanceVerdictAllow         = "ALLOW"
	governanceVerdictDeny          = "DENY"
	governanceVerdictConstrain     = "ALLOW_WITH_CONSTRAINTS"
	governanceVerdictRequire       = "REQUIRE_APPROVAL"
	governanceVerdictThrottle      = "THROTTLE"
)

type governanceRuntime struct {
	redisURL    string
	rawClient   redis.UniversalClient
	decisionTTL time.Duration
}

type governanceBackfillConfig struct {
	Since         *time.Time
	Until         *time.Time
	DryRun        bool
	ProgressEvery int
}

type governanceBackfillSummary struct {
	DryRun          bool   `json:"dry_run"`
	Since           string `json:"since,omitempty"`
	Until           string `json:"until,omitempty"`
	StartedAt       string `json:"started_at"`
	CompletedAt     string `json:"completed_at"`
	ScannedJobs     int    `json:"scanned_jobs"`
	MissingDecision int    `json:"missing_decision"`
	MissingTime     int    `json:"missing_time"`
	OutOfRange      int    `json:"out_of_range"`
	SkippedExisting int    `json:"skipped_existing"`
	WouldAppend     int    `json:"would_append"`
	Appended        int    `json:"appended"`
}

type governanceTailSummary struct {
	StartedAt       string `json:"started_at"`
	CompletedAt     string `json:"completed_at"`
	Processed       int    `json:"processed"`
	Ignored         int    `json:"ignored"`
	SkippedExisting int    `json:"skipped_existing"`
	Appended        int    `json:"appended"`
	Errors          int    `json:"errors"`
}

type governanceAuditSubscriber interface {
	Subscribe(handler func([]byte) error) error
}

type governanceDecisionLogRecord struct {
	JobID            string          `json:"job_id,omitempty"`
	Tenant           string          `json:"tenant,omitempty"`
	AgentID          string          `json:"agent_id,omitempty"`
	Topic            string          `json:"topic,omitempty"`
	Verdict          string          `json:"verdict,omitempty"`
	RuleID           string          `json:"rule_id,omitempty"`
	PolicyVersion    string          `json:"policy_version,omitempty"`
	Reason           string          `json:"reason,omitempty"`
	Constraints      json.RawMessage `json:"constraints,omitempty"`
	ApprovalStatus   string          `json:"approval_status,omitempty"`
	ApprovalDecision string          `json:"approval_decision,omitempty"`
	Timestamp        int64           `json:"timestamp,omitempty"`
}

type governanceSafetyDecision struct {
	Decision         string
	Reason           string
	RuleID           string
	PolicySnapshot   string
	Constraints      json.RawMessage
	ApprovalStatus   string
	ApprovalDecision string
	CheckedAt        int64
}

type governanceJobRequest struct {
	JobID    string             `json:"jobId,omitempty"`
	Topic    string             `json:"topic,omitempty"`
	TenantID string             `json:"tenantId,omitempty"`
	Env      map[string]string  `json:"env,omitempty"`
	Labels   map[string]string  `json:"labels,omitempty"`
	Meta     *governanceJobMeta `json:"meta,omitempty"`
}

type governanceJobMeta struct {
	Labels map[string]string `json:"labels,omitempty"`
}

type governanceAuditEvent struct {
	Timestamp     time.Time         `json:"timestamp"`
	EventType     string            `json:"event_type"`
	TenantID      string            `json:"tenant_id"`
	AgentID       string            `json:"agent_id,omitempty"`
	JobID         string            `json:"job_id,omitempty"`
	Decision      string            `json:"decision,omitempty"`
	MatchedRule   string            `json:"matched_rule,omitempty"`
	Reason        string            `json:"reason,omitempty"`
	PolicyVersion string            `json:"policy_version,omitempty"`
	Extra         map[string]string `json:"extra,omitempty"`
}

type natsGovernanceSubscriber struct {
	conn     *nats.Conn
	progress io.Writer
}

func runGovernanceCmd(args []string) {
	if len(args) < 1 {
		usage()
		os.Exit(1)
	}
	switch args[0] {
	case "backfill-decisions":
		runGovernanceBackfillCmd(args[1:])
	case "tail":
		runGovernanceTailCmd(args[1:])
	default:
		usage()
		os.Exit(1)
	}
}

func runGovernanceBackfillCmd(args []string) {
	fs := flag.NewFlagSet("governance backfill-decisions", flag.ExitOnError)
	redisURL := fs.String("redis-url", envOr("REDIS_URL", governanceDefaultRedisURL), "Redis URL")
	sinceRaw := fs.String("since", "", "inclusive lower date bound (YYYY-MM-DD)")
	untilRaw := fs.String("until", "", "inclusive upper date bound (YYYY-MM-DD)")
	dryRun := fs.Bool("dry-run", false, "scan decisions without writing to the Policy Decision Log")
	if err := fs.Parse(args); err != nil {
		fail(err.Error())
	}

	since, err := parseGovernanceDate(*sinceRaw, false)
	check(err)
	until, err := parseGovernanceDate(*untilRaw, true)
	check(err)
	if since != nil && until != nil && until.Before(*since) {
		fail("--until must be on or after --since")
	}

	runtime, err := newGovernanceRuntime(*redisURL)
	check(err)
	defer func() { _ = runtime.Close() }()

	summary, err := runGovernanceBackfill(context.Background(), runtime, governanceBackfillConfig{Since: since, Until: until, DryRun: *dryRun, ProgressEvery: 100}, os.Stderr)
	check(err)
	printJSON(summary)
}

func runGovernanceTailCmd(args []string) {
	fs := flag.NewFlagSet("governance tail", flag.ExitOnError)
	redisURL := fs.String("redis-url", envOr("REDIS_URL", governanceDefaultRedisURL), "Redis URL")
	natsURL := fs.String("nats-url", envOr("NATS_URL", governanceDefaultNATSURL), "NATS URL")
	if err := fs.Parse(args); err != nil {
		fail(err.Error())
	}

	runtime, err := newGovernanceRuntime(*redisURL)
	check(err)
	defer func() { _ = runtime.Close() }()

	conn, err := nats.Connect(*natsURL)
	check(err)
	defer conn.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		_ = conn.Drain()
		conn.Close()
	}()

	summary, err := runGovernanceTail(ctx, natsGovernanceSubscriber{conn: conn, progress: os.Stderr}, runtime, os.Stderr)
	if err != nil && !errors.Is(err, context.Canceled) {
		check(err)
	}
	printJSON(summary)
}

func newGovernanceRuntime(redisURL string) (*governanceRuntime, error) {
	redisURL = strings.TrimSpace(redisURL)
	if redisURL == "" {
		redisURL = governanceDefaultRedisURL
	}
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	client := redis.NewClient(options)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("connect redis: %w", err)
	}
	return &governanceRuntime{redisURL: redisURL, rawClient: client, decisionTTL: governanceDecisionTTL()}, nil
}

func (g *governanceRuntime) Close() error {
	if g == nil || g.rawClient == nil {
		return nil
	}
	return g.rawClient.Close()
}

func runGovernanceBackfill(ctx context.Context, runtime *governanceRuntime, cfg governanceBackfillConfig, progress io.Writer) (governanceBackfillSummary, error) {
	summary := governanceBackfillSummary{DryRun: cfg.DryRun, StartedAt: time.Now().UTC().Format(time.RFC3339)}
	defer func() { summary.CompletedAt = time.Now().UTC().Format(time.RFC3339) }()
	if cfg.Since != nil {
		summary.Since = cfg.Since.UTC().Format(time.RFC3339)
	}
	if cfg.Until != nil {
		summary.Until = cfg.Until.UTC().Format(time.RFC3339)
	}

	jobIDs, err := runtime.scanJobIDs(ctx)
	if err != nil {
		return summary, err
	}
	for _, jobID := range jobIDs {
		if err := ctx.Err(); err != nil {
			return summary, err
		}
		summary.ScannedJobs++

		req, err := runtime.loadJobRequest(ctx, jobID)
		if err != nil {
			if isRedisNilErr(err) {
				continue
			}
			return summary, fmt.Errorf("load job request %s: %w", jobID, err)
		}
		safety, err := runtime.loadSafetyDecision(ctx, jobID)
		if err != nil {
			return summary, fmt.Errorf("load safety decision %s: %w", jobID, err)
		}
		if strings.TrimSpace(safety.Decision) == "" {
			summary.MissingDecision++
			governanceProgressf(progress, cfg.ProgressEvery, summary.ScannedJobs, "missing safety decision", summary)
			continue
		}

		timestamp := governanceDecisionTimestampMillis(safety.CheckedAt)
		if timestamp <= 0 {
			summary.MissingTime++
			governanceProgressf(progress, cfg.ProgressEvery, summary.ScannedJobs, "missing decision timestamp", summary)
			continue
		}
		if !governanceTimestampInWindow(timestamp, cfg.Since, cfg.Until) {
			summary.OutOfRange++
			governanceProgressf(progress, cfg.ProgressEvery, summary.ScannedJobs, "outside requested window", summary)
			continue
		}

		record, err := governanceDecisionRecordFromJob(req, safety, timestamp)
		if err != nil {
			return summary, fmt.Errorf("project decision log record %s: %w", jobID, err)
		}
		exists, err := runtime.decisionRecordExists(ctx, record)
		if err != nil {
			return summary, err
		}
		if exists {
			summary.SkippedExisting++
			governanceProgressf(progress, cfg.ProgressEvery, summary.ScannedJobs, "already present", summary)
			continue
		}
		if cfg.DryRun {
			summary.WouldAppend++
			governanceProgressf(progress, cfg.ProgressEvery, summary.ScannedJobs, "dry-run candidate", summary)
			continue
		}
		if err := runtime.appendDecisionRecord(ctx, record); err != nil {
			return summary, fmt.Errorf("append decision log record %s: %w", jobID, err)
		}
		summary.Appended++
		governanceProgressf(progress, cfg.ProgressEvery, summary.ScannedJobs, "appended", summary)
	}

	governanceWriteProgress(progress, fmt.Sprintf("backfill complete: scanned=%d appended=%d skipped_existing=%d dry_run=%t\n", summary.ScannedJobs, summary.Appended, summary.SkippedExisting, summary.DryRun))
	return summary, nil
}

func runGovernanceTail(ctx context.Context, subscriber governanceAuditSubscriber, runtime *governanceRuntime, progress io.Writer) (governanceTailSummary, error) {
	summary := governanceTailSummary{StartedAt: time.Now().UTC().Format(time.RFC3339)}
	defer func() { summary.CompletedAt = time.Now().UTC().Format(time.RFC3339) }()

	var mu sync.Mutex
	if err := subscriber.Subscribe(func(data []byte) error {
		record, ignore, err := governanceDecisionRecordFromAuditMessage(data)
		mu.Lock()
		defer mu.Unlock()
		if ignore {
			summary.Ignored++
			return nil
		}
		if err != nil {
			summary.Errors++
			governanceWriteProgress(progress, fmt.Sprintf("tail skipped malformed audit packet: %v\n", err))
			return nil
		}
		exists, existsErr := runtime.decisionRecordExists(context.Background(), record)
		if existsErr != nil {
			summary.Errors++
			return existsErr
		}
		if exists {
			summary.Processed++
			summary.SkippedExisting++
			return nil
		}
		if appendErr := runtime.appendDecisionRecord(context.Background(), record); appendErr != nil {
			summary.Errors++
			return appendErr
		}
		summary.Processed++
		summary.Appended++
		if summary.Processed == 1 || summary.Processed%25 == 0 {
			governanceWriteProgress(progress, fmt.Sprintf("tail progress: processed=%d appended=%d skipped_existing=%d ignored=%d\n", summary.Processed, summary.Appended, summary.SkippedExisting, summary.Ignored))
		}
		return nil
	}); err != nil {
		return summary, fmt.Errorf("subscribe governance tail: %w", err)
	}

	<-ctx.Done()
	governanceWriteProgress(progress, fmt.Sprintf("tail stopped: processed=%d appended=%d skipped_existing=%d ignored=%d errors=%d\n", summary.Processed, summary.Appended, summary.SkippedExisting, summary.Ignored, summary.Errors))
	return summary, ctx.Err()
}

func (s natsGovernanceSubscriber) Subscribe(handler func([]byte) error) error {
	_, err := s.conn.QueueSubscribe(governanceAuditSubject, governanceTailQueue, func(msg *nats.Msg) {
		if err := handler(msg.Data); err != nil {
			governanceWriteProgress(s.progress, fmt.Sprintf("tail handler error: %v\n", err))
		}
	})
	if err != nil {
		return err
	}
	_ = s.conn.Flush()
	return s.conn.LastError()
}

func (g *governanceRuntime) scanJobIDs(ctx context.Context) ([]string, error) {
	ids := make(map[string]struct{})
	var cursor uint64
	for {
		keys, next, err := g.rawClient.Scan(ctx, cursor, governanceJobRequestPrefix+"*", governanceScanBatchSize).Result()
		if err != nil {
			return nil, fmt.Errorf("scan job request keys: %w", err)
		}
		for _, key := range keys {
			jobID := strings.TrimSpace(strings.TrimPrefix(key, governanceJobRequestPrefix))
			if jobID != "" {
				ids[jobID] = struct{}{}
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	out := make([]string, 0, len(ids))
	for jobID := range ids {
		out = append(out, jobID)
	}
	sort.Strings(out)
	return out, nil
}

func (g *governanceRuntime) loadJobRequest(ctx context.Context, jobID string) (*governanceJobRequest, error) {
	data, err := g.rawClient.Get(ctx, governanceJobRequestKey(jobID)).Bytes()
	if err != nil {
		return nil, err
	}
	var req governanceJobRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, fmt.Errorf("decode job request: %w", err)
	}
	return &req, nil
}

func (g *governanceRuntime) loadSafetyDecision(ctx context.Context, jobID string) (governanceSafetyDecision, error) {
	data, err := g.rawClient.HGetAll(ctx, governanceJobMetaKey(jobID)).Result()
	if err != nil && err != redis.Nil {
		return governanceSafetyDecision{}, err
	}
	record := governanceSafetyDecision{
		Decision:         strings.TrimSpace(data[governanceMetaSafetyDecision]),
		Reason:           strings.TrimSpace(data[governanceMetaSafetyReason]),
		RuleID:           strings.TrimSpace(data[governanceMetaSafetyRuleID]),
		PolicySnapshot:   strings.TrimSpace(data[governanceMetaSafetySnapshot]),
		ApprovalStatus:   strings.TrimSpace(data[governanceMetaApprovalStatus]),
		ApprovalDecision: strings.TrimSpace(data[governanceMetaApprovalDecision]),
	}
	if raw := strings.TrimSpace(data[governanceMetaSafetyChecked]); raw != "" {
		parsed, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil {
			return governanceSafetyDecision{}, fmt.Errorf("parse checked_at: %w", parseErr)
		}
		record.CheckedAt = parsed
	}
	if raw := strings.TrimSpace(data[governanceMetaSafetyConst]); raw != "" {
		if !json.Valid([]byte(raw)) {
			return governanceSafetyDecision{}, fmt.Errorf("decode constraints: invalid json")
		}
		record.Constraints = json.RawMessage(raw)
	}
	return record, nil
}

func (g *governanceRuntime) appendDecisionRecord(ctx context.Context, record governanceDecisionLogRecord) error {
	record = governanceNormalizeDecisionRecord(record)
	if err := governanceValidateDecisionRecord(record); err != nil {
		return err
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal decision record: %w", err)
	}
	recordID := governanceDecisionRecordID(record)
	recordKey := governanceDecisionRecordKey(record.Tenant, recordID)
	primaryIndex := governanceDecisionPrimaryIndexKey(record.Tenant)
	verdictWire, err := governanceDecisionWireVerdict(record.Verdict)
	if err != nil {
		return err
	}
	pipe := g.rawClient.TxPipeline()
	score := float64(record.Timestamp)
	pipe.HSet(ctx, recordKey, governanceDecisionJSONField, payload)
	pipe.ZAdd(ctx, primaryIndex, redis.Z{Score: score, Member: recordID})
	if record.RuleID != "" {
		pipe.ZAdd(ctx, governanceDecisionRuleIndexKey(record.Tenant, record.RuleID), redis.Z{Score: score, Member: recordID})
	}
	if record.AgentID != "" {
		pipe.ZAdd(ctx, governanceDecisionAgentIndexKey(record.Tenant, record.AgentID), redis.Z{Score: score, Member: recordID})
	}
	if record.Topic != "" {
		pipe.ZAdd(ctx, governanceDecisionTopicIndexKey(record.Tenant, record.Topic), redis.Z{Score: score, Member: recordID})
	}
	pipe.ZAdd(ctx, governanceDecisionVerdictIndexKey(record.Tenant, verdictWire), redis.Z{Score: score, Member: recordID})
	if g.decisionTTL > 0 {
		pipe.Expire(ctx, recordKey, g.decisionTTL)
		cutoff := time.Now().UTC().Add(-g.decisionTTL).UnixMilli()
		min, max := "-inf", strconv.FormatInt(cutoff, 10)
		pipe.ZRemRangeByScore(ctx, primaryIndex, min, max)
		if record.RuleID != "" {
			pipe.ZRemRangeByScore(ctx, governanceDecisionRuleIndexKey(record.Tenant, record.RuleID), min, max)
		}
		if record.AgentID != "" {
			pipe.ZRemRangeByScore(ctx, governanceDecisionAgentIndexKey(record.Tenant, record.AgentID), min, max)
		}
		if record.Topic != "" {
			pipe.ZRemRangeByScore(ctx, governanceDecisionTopicIndexKey(record.Tenant, record.Topic), min, max)
		}
		pipe.ZRemRangeByScore(ctx, governanceDecisionVerdictIndexKey(record.Tenant, verdictWire), min, max)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("append decision record: %w", err)
	}
	return nil
}

func (g *governanceRuntime) decisionRecordExists(ctx context.Context, record governanceDecisionLogRecord) (bool, error) {
	exists, err := g.rawClient.Exists(ctx, governanceDecisionRecordKey(record.Tenant, governanceDecisionRecordID(record))).Result()
	if err != nil {
		return false, fmt.Errorf("check decision log record %s: %w", record.JobID, err)
	}
	return exists > 0, nil
}

func governanceDecisionRecordFromJob(req *governanceJobRequest, safety governanceSafetyDecision, timestamp int64) (governanceDecisionLogRecord, error) {
	if req == nil {
		return governanceDecisionLogRecord{}, fmt.Errorf("job request is required")
	}
	record := governanceDecisionLogRecord{
		JobID:            strings.TrimSpace(req.JobID),
		Tenant:           governanceExtractTenant(req),
		AgentID:          governanceAgentID(req),
		Topic:            strings.TrimSpace(req.Topic),
		Verdict:          strings.TrimSpace(safety.Decision),
		RuleID:           strings.TrimSpace(safety.RuleID),
		PolicyVersion:    governancePolicyVersion(safety.PolicySnapshot),
		Reason:           strings.TrimSpace(safety.Reason),
		Constraints:      governanceNormalizeRawJSON(safety.Constraints),
		ApprovalStatus:   strings.TrimSpace(safety.ApprovalStatus),
		ApprovalDecision: strings.TrimSpace(safety.ApprovalDecision),
		Timestamp:        timestamp,
	}
	return record, governanceValidateDecisionRecord(record)
}

func governanceDecisionRecordFromAuditMessage(data []byte) (governanceDecisionLogRecord, bool, error) {
	message, source, ignore, err := governanceExtractAlert(data)
	if ignore || err != nil {
		return governanceDecisionLogRecord{}, ignore, err
	}
	if strings.TrimSpace(source) != "audit-export" {
		return governanceDecisionLogRecord{}, true, nil
	}
	var event governanceAuditEvent
	if err := json.Unmarshal([]byte(message), &event); err != nil {
		return governanceDecisionLogRecord{}, false, fmt.Errorf("decode audit event: %w", err)
	}
	if strings.TrimSpace(event.EventType) != "safety.decision" {
		return governanceDecisionLogRecord{}, true, nil
	}
	verdict, err := governanceCanonicalVerdict(event.Decision)
	if err != nil {
		return governanceDecisionLogRecord{}, false, err
	}
	record := governanceDecisionLogRecord{
		JobID:            strings.TrimSpace(event.JobID),
		Tenant:           strings.TrimSpace(event.TenantID),
		AgentID:          governanceFirstNonEmpty(strings.TrimSpace(event.AgentID), governanceExtraField(event.Extra, "agent_id")),
		Topic:            governanceExtraField(event.Extra, "topic", "job_topic"),
		Verdict:          verdict,
		RuleID:           strings.TrimSpace(event.MatchedRule),
		PolicyVersion:    strings.TrimSpace(event.PolicyVersion),
		Reason:           strings.TrimSpace(event.Reason),
		Constraints:      governanceNormalizeRawJSON([]byte(governanceExtraField(event.Extra, "constraints", "policy_constraints"))),
		ApprovalStatus:   governanceExtraField(event.Extra, "approval_status"),
		ApprovalDecision: governanceExtraField(event.Extra, "approval_decision"),
		Timestamp:        event.Timestamp.UTC().UnixMilli(),
	}
	if err := governanceValidateDecisionRecord(record); err != nil {
		return governanceDecisionLogRecord{}, false, err
	}
	return record, false, nil
}

func governanceExtractAlert(data []byte) (message string, source string, ignore bool, err error) {
	for len(data) > 0 {
		num, typ, n := protowire.ConsumeTag(data)
		if n < 0 {
			return "", "", false, protowire.ParseError(n)
		}
		data = data[n:]
		if num == 13 {
			if typ != protowire.BytesType {
				return "", "", false, fmt.Errorf("unexpected alert wire type %d", typ)
			}
			payload, m := protowire.ConsumeBytes(data)
			if m < 0 {
				return "", "", false, protowire.ParseError(m)
			}
			return governanceExtractSystemAlert(payload)
		}
		m := protowire.ConsumeFieldValue(num, typ, data)
		if m < 0 {
			return "", "", false, protowire.ParseError(m)
		}
		data = data[m:]
	}
	return "", "", true, nil
}

func governanceExtractSystemAlert(data []byte) (message string, source string, ignore bool, err error) {
	for len(data) > 0 {
		num, typ, n := protowire.ConsumeTag(data)
		if n < 0 {
			return "", "", false, protowire.ParseError(n)
		}
		data = data[n:]
		switch num {
		case 2:
			if typ != protowire.BytesType {
				return "", "", false, fmt.Errorf("unexpected alert.message wire type %d", typ)
			}
			value, m := protowire.ConsumeString(data)
			if m < 0 {
				return "", "", false, protowire.ParseError(m)
			}
			message = value
			data = data[m:]
		case 7:
			if typ != protowire.BytesType {
				return "", "", false, fmt.Errorf("unexpected alert.source_component wire type %d", typ)
			}
			value, m := protowire.ConsumeString(data)
			if m < 0 {
				return "", "", false, protowire.ParseError(m)
			}
			source = value
			data = data[m:]
		default:
			m := protowire.ConsumeFieldValue(num, typ, data)
			if m < 0 {
				return "", "", false, protowire.ParseError(m)
			}
			data = data[m:]
		}
	}
	return message, source, false, nil
}

func parseGovernanceDate(raw string, endOfDay bool) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parsed, err := time.ParseInLocation("2006-01-02", raw, time.UTC)
	if err != nil {
		return nil, fmt.Errorf("parse governance date %q: %w", raw, err)
	}
	if endOfDay {
		value := parsed.Add(24*time.Hour - time.Millisecond)
		return &value, nil
	}
	return &parsed, nil
}

func governanceTimestampInWindow(timestamp int64, since *time.Time, until *time.Time) bool {
	if since != nil && timestamp < since.UTC().UnixMilli() {
		return false
	}
	if until != nil && timestamp > until.UTC().UnixMilli() {
		return false
	}
	return true
}

func governanceDecisionTimestampMillis(checkedAt int64) int64 {
	switch {
	case checkedAt <= 0:
		return 0
	case checkedAt < 1_000_000_000_000:
		return checkedAt * 1000
	case checkedAt < 1_000_000_000_000_000:
		return checkedAt
	case checkedAt < 1_000_000_000_000_000_000:
		return checkedAt / 1000
	default:
		return checkedAt / 1_000_000
	}
}

func governanceExtractTenant(req *governanceJobRequest) string {
	if req == nil {
		return "default"
	}
	if tenant := strings.TrimSpace(req.TenantID); tenant != "" {
		return tenant
	}
	if req.Env != nil {
		if tenant := strings.TrimSpace(req.Env["tenant_id"]); tenant != "" {
			return tenant
		}
	}
	return "default"
}

func governanceAgentID(req *governanceJobRequest) string {
	if req == nil {
		return ""
	}
	if req.Labels != nil {
		if agentID := strings.TrimSpace(req.Labels["agent_id"]); agentID != "" {
			return agentID
		}
	}
	if req.Meta != nil && req.Meta.Labels != nil {
		return strings.TrimSpace(req.Meta.Labels["agent_id"])
	}
	return ""
}

func governancePolicyVersion(snapshot string) string {
	snapshot = strings.TrimSpace(snapshot)
	if snapshot == "" {
		return ""
	}
	if idx := strings.Index(snapshot, "|"); idx >= 0 {
		return snapshot[:idx]
	}
	return snapshot
}

func governanceNormalizeDecisionRecord(record governanceDecisionLogRecord) governanceDecisionLogRecord {
	record.JobID = strings.TrimSpace(record.JobID)
	record.Tenant = strings.TrimSpace(record.Tenant)
	record.AgentID = strings.TrimSpace(record.AgentID)
	record.Topic = strings.TrimSpace(record.Topic)
	record.Verdict = strings.TrimSpace(record.Verdict)
	record.RuleID = strings.TrimSpace(record.RuleID)
	record.PolicyVersion = strings.TrimSpace(record.PolicyVersion)
	record.Reason = strings.TrimSpace(record.Reason)
	record.ApprovalStatus = strings.TrimSpace(record.ApprovalStatus)
	record.ApprovalDecision = strings.TrimSpace(record.ApprovalDecision)
	record.Constraints = governanceNormalizeRawJSON(record.Constraints)
	return record
}

func governanceValidateDecisionRecord(record governanceDecisionLogRecord) error {
	if strings.TrimSpace(record.Tenant) == "" {
		return fmt.Errorf("decision log record tenant is required")
	}
	if strings.TrimSpace(record.JobID) == "" {
		return fmt.Errorf("decision log record job id is required")
	}
	if record.Timestamp <= 0 {
		return fmt.Errorf("decision log record timestamp is required")
	}
	_, err := governanceDecisionWireVerdict(record.Verdict)
	return err
}

func governanceCanonicalVerdict(raw string) (string, error) {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case strings.ToLower(governanceVerdictAllow), "allow":
		return governanceVerdictAllow, nil
	case strings.ToLower(governanceVerdictDeny), "deny":
		return governanceVerdictDeny, nil
	case strings.ToLower(governanceVerdictConstrain), "constrain":
		return governanceVerdictConstrain, nil
	case strings.ToLower(governanceVerdictRequire), "require_approval":
		return governanceVerdictRequire, nil
	case strings.ToLower(governanceVerdictThrottle), "throttle":
		return governanceVerdictThrottle, nil
	default:
		return "", fmt.Errorf("unknown decision verdict %q", raw)
	}
}

func governanceDecisionWireVerdict(verdict string) (string, error) {
	switch strings.TrimSpace(verdict) {
	case governanceVerdictAllow:
		return "allow", nil
	case governanceVerdictDeny:
		return "deny", nil
	case governanceVerdictConstrain:
		return "constrain", nil
	case governanceVerdictRequire:
		return "require_approval", nil
	case governanceVerdictThrottle:
		return "throttle", nil
	default:
		return "", fmt.Errorf("unknown decision verdict %q", verdict)
	}
}

func governanceDecisionTTL() time.Duration {
	raw := strings.TrimSpace(os.Getenv(governanceDecisionTTLSeconds))
	if raw == "" {
		return governanceDefaultDecisionTTL
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		return governanceDefaultDecisionTTL
	}
	return time.Duration(seconds) * time.Second
}

func governanceDecisionRecordID(record governanceDecisionLogRecord) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{strings.TrimSpace(record.Tenant), strings.TrimSpace(record.JobID), strconv.FormatInt(record.Timestamp, 10)}, "|")))
	return hex.EncodeToString(sum[:])
}

func governanceJobRequestKey(jobID string) string {
	return governanceJobRequestPrefix + strings.TrimSpace(jobID)
}

func governanceJobMetaKey(jobID string) string {
	return governanceJobMetaPrefix + strings.TrimSpace(jobID)
}

func governanceDecisionTenantPrefix(tenant string) string {
	return governanceDecisionPrefix + strings.TrimSpace(tenant) + ":"
}

func governanceDecisionPrimaryIndexKey(tenant string) string {
	return governanceDecisionTenantPrefix(tenant) + "idx:t"
}

func governanceDecisionRecordKey(tenant, id string) string {
	return governanceDecisionTenantPrefix(tenant) + "rec:" + strings.TrimSpace(id)
}

func governanceDecisionRuleIndexKey(tenant, ruleID string) string {
	return governanceDecisionTenantPrefix(tenant) + "idx:rule:" + strings.TrimSpace(ruleID)
}

func governanceDecisionAgentIndexKey(tenant, agentID string) string {
	return governanceDecisionTenantPrefix(tenant) + "idx:agent:" + strings.TrimSpace(agentID)
}

func governanceDecisionTopicIndexKey(tenant, topic string) string {
	return governanceDecisionTenantPrefix(tenant) + "idx:topic:" + strings.TrimSpace(topic)
}

func governanceDecisionVerdictIndexKey(tenant, verdict string) string {
	return governanceDecisionTenantPrefix(tenant) + "idx:verdict:" + strings.TrimSpace(verdict)
}

func governanceNormalizeRawJSON(raw []byte) json.RawMessage {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	if !json.Valid([]byte(trimmed)) {
		return nil
	}
	return json.RawMessage(trimmed)
}

func governanceExtraField(extra map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(extra[key]); value != "" {
			return value
		}
	}
	return ""
}

func governanceFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func governanceProgressf(w io.Writer, every int, scanned int, reason string, summary governanceBackfillSummary) {
	if every <= 0 || scanned == 0 || scanned%every != 0 {
		return
	}
	governanceWriteProgress(w, fmt.Sprintf("backfill progress: scanned=%d appended=%d skipped_existing=%d would_append=%d missing_decision=%d missing_time=%d out_of_range=%d (%s)\n", summary.ScannedJobs, summary.Appended, summary.SkippedExisting, summary.WouldAppend, summary.MissingDecision, summary.MissingTime, summary.OutOfRange, reason))
}

func governanceWriteProgress(w io.Writer, line string) {
	if w == nil || strings.TrimSpace(line) == "" {
		return
	}
	_, _ = io.WriteString(w, line)
}

func isRedisNilErr(err error) bool {
	return errors.Is(err, redis.Nil) || strings.Contains(strings.ToLower(err.Error()), "redis: nil")
}
