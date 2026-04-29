package extraction

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/redis/go-redis/v9"
)

const (
	decisionPageSize    = 500
	jobRequestCacheSize = 1024
)

var (
	extractionTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "evals_extraction_total",
		Help: "Total decision-log incidents consumed by the eval extraction pipeline.",
	}, []string{"tenant", "verdict"})

	extractionDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "evals_extraction_duration_seconds",
		Help:    "Duration of incident-to-dataset extraction runs.",
		Buckets: prometheus.DefBuckets,
	})

	extractionEntriesDedupedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "evals_extraction_entries_deduped_total",
		Help: "Total number of duplicate incidents collapsed during extraction.",
	}, []string{"tenant"})
)

type projectedDecision struct {
	record    model.DecisionLogRecord
	snapshot  inputSnapshot
	entry     model.EvalEntry
	dedupeKey string
}

type topicMatcher struct {
	exact string
	match func(string) bool
}

func (s *Service) preview(ctx context.Context, req ExtractionRequest) (ExtractionResult, error) {
	start := time.Now()
	defer func() { extractionDuration.Observe(time.Since(start).Seconds()) }()

	matcher, err := compileTopicMatcher(req.TopicPattern)
	if err != nil {
		return ExtractionResult{}, err
	}

	scanned, projected, warnings, countsByVerdict, err := s.scanAndProject(ctx, req, matcher)
	recordExtractionMetrics(req.Tenant, countsByVerdict)

	entries, dedupedCount := dedupeProjected(projected)
	if dedupedCount > 0 {
		extractionEntriesDedupedTotal.WithLabelValues(req.Tenant).Add(float64(dedupedCount))
	}
	if len(entries) > req.MaxEntries {
		warnings = append(warnings, fmt.Sprintf("truncated to max_entries=%d after dedupe", req.MaxEntries))
		entries = entries[:req.MaxEntries]
	}

	result := ExtractionResult{
		Name:             req.DatasetName,
		EntryCount:       len(entries),
		DedupedCount:     dedupedCount,
		ScannedDecisions: scanned,
		Warnings:         warnings,
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return result, &TimeoutError{Result: result, Err: err}
		}
		return ExtractionResult{}, err
	}
	if req.DryRun || len(entries) == 0 {
		return result, nil
	}

	created, err := s.commitDataset(ctx, req, entries)
	if err != nil {
		return ExtractionResult{}, err
	}
	result.DatasetID = created.ID
	result.Version = created.Version
	result.EntryCount = created.EntryCount
	return result, nil
}

func (s *Service) scanAndProject(ctx context.Context, req ExtractionRequest, matcher topicMatcher) (int, []projectedDecision, []string, map[model.SafetyDecision]int, error) {
	cache := newJobRequestCache(jobRequestCacheSize)
	warnings := make([]string, 0)
	countsByVerdict := make(map[model.SafetyDecision]int, len(req.Verdicts))
	maxEntries := boundedExtractionMaxEntries(req.MaxEntries)
	projected := make([]projectedDecision, 0, model.MaxEvalDatasetEntries)
	candidateCap := maxEntries * 3
	if candidateCap < maxEntries {
		candidateCap = maxEntries
	}

	scanned := 0
	for _, verdict := range req.Verdicts {
		if err := ctx.Err(); err != nil {
			return scanned, projected, warnings, countsByVerdict, err
		}
		if scanned >= candidateCap {
			break
		}
		cursor := ""
		for scanned < candidateCap {
			if err := ctx.Err(); err != nil {
				return scanned, projected, warnings, countsByVerdict, err
			}
			remaining := candidateCap - scanned
			limit := decisionPageSize
			if remaining < limit {
				limit = remaining
			}
			if limit <= 0 {
				break
			}

			query := model.DecisionQuery{
				Tenant:  req.Tenant,
				Since:   req.Since.UnixMilli(),
				Until:   req.Until.UnixMilli(),
				Topic:   matcher.exact,
				RuleID:  req.RuleID,
				Verdict: verdict,
				AgentID: req.AgentID,
				Cursor:  cursor,
				Limit:   limit,
			}
			page, err := s.deps.DecisionLog.QueryDecisions(ctx, query)
			if err != nil {
				return scanned, projected, warnings, countsByVerdict, fmt.Errorf("query decisions for verdict %q: %w", verdict, err)
			}
			if len(page.Items) == 0 {
				break
			}

			for _, record := range page.Items {
				if err := ctx.Err(); err != nil {
					return scanned, projected, warnings, countsByVerdict, err
				}
				if !matcher.matches(record.Topic) {
					continue
				}
				scanned++
				countsByVerdict[record.Verdict]++

				projectedDecision, warning, ok, err := s.projectDecision(ctx, cache, record)
				if err != nil {
					return scanned, projected, warnings, countsByVerdict, err
				}
				if warning != "" {
					warnings = append(warnings, warning)
				}
				if ok {
					projected = append(projected, projectedDecision)
				}
				if scanned >= candidateCap {
					warnings = append(warnings, fmt.Sprintf("candidate scan capped at %d decisions", candidateCap))
					break
				}
			}

			if page.NextCursor == "" || scanned >= candidateCap {
				break
			}
			cursor = page.NextCursor
		}
	}

	return scanned, projected, dedupeWarnings(warnings), countsByVerdict, nil
}

func boundedExtractionMaxEntries(maxEntries int) int {
	if maxEntries <= 0 {
		return DefaultMaxEntries
	}
	if maxEntries > model.MaxEvalDatasetEntries {
		return model.MaxEvalDatasetEntries
	}
	return maxEntries
}

func (s *Service) projectDecision(ctx context.Context, cache *jobRequestCache, record model.DecisionLogRecord) (projectedDecision, string, bool, error) {
	req, err := cache.getOrLoad(ctx, strings.TrimSpace(record.JobID), s.deps.JobStore)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return projectedDecision{}, fmt.Sprintf("job %s missing from job store; skipped incident", record.JobID), false, nil
		}
		return projectedDecision{}, "", false, fmt.Errorf("load job request %s: %w", record.JobID, err)
	}
	snapshot, inputJSON, err := buildInputSnapshot(req)
	if err != nil {
		return projectedDecision{}, "", false, fmt.Errorf("build snapshot for job %s: %w", record.JobID, err)
	}

	key, err := buildDedupeKey(record, snapshot)
	if err != nil {
		return projectedDecision{}, "", false, fmt.Errorf("build dedupe key for job %s: %w", record.JobID, err)
	}

	entry := model.EvalEntry{
		ID:               buildEvalEntryID(key),
		Input:            inputJSON,
		ExpectedDecision: record.Verdict,
		RuleID:           strings.TrimSpace(record.RuleID),
		Metadata:         buildDecisionMetadata(record),
		Source:           model.EvalEntrySourceAuditImport,
		SourceRef:        buildSourceRef(record),
	}
	return projectedDecision{
		record:    record,
		snapshot:  snapshot,
		entry:     entry,
		dedupeKey: key,
	}, "", true, nil
}

func (s *Service) commitDataset(ctx context.Context, req ExtractionRequest, entries []model.EvalEntry) (model.EvalDataset, error) {
	versions, err := s.deps.EvalDatasets.ListEvalDatasetVersions(ctx, req.Tenant, req.DatasetName)
	if err != nil {
		return model.EvalDataset{}, fmt.Errorf("list dataset versions: %w", err)
	}
	nextVersion := 1
	for _, version := range versions {
		if version.Version >= nextVersion {
			nextVersion = version.Version + 1
		}
	}

	dataset := model.EvalDataset{
		Name:        req.DatasetName,
		Version:     nextVersion,
		Tenant:      req.Tenant,
		Description: req.DatasetDescription,
		Entries:     entries,
	}

	created, err := s.deps.EvalDatasets.CreateEvalDataset(ctx, dataset)
	if err == nil {
		return created, nil
	}
	if !errors.Is(err, store.ErrEvalDatasetVersionExists) {
		return model.EvalDataset{}, fmt.Errorf("create eval dataset: %w", err)
	}

	dataset.Version++
	created, retryErr := s.deps.EvalDatasets.CreateEvalDataset(ctx, dataset)
	if retryErr != nil {
		return model.EvalDataset{}, fmt.Errorf("create eval dataset after version retry: %w", retryErr)
	}
	return created, nil
}

func compileTopicMatcher(pattern string) (topicMatcher, error) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return topicMatcher{match: func(string) bool { return true }}, nil
	}
	if strings.HasPrefix(pattern, "re:") {
		re, err := regexp.Compile(strings.TrimSpace(strings.TrimPrefix(pattern, "re:")))
		if err != nil {
			return topicMatcher{}, fmt.Errorf("invalid topic pattern: %w", err)
		}
		return topicMatcher{match: re.MatchString}, nil
	}
	if strings.ContainsAny(pattern, "*?[]") {
		return topicMatcher{
			match: func(topic string) bool {
				ok, err := path.Match(pattern, strings.TrimSpace(topic))
				return err == nil && ok
			},
		}, nil
	}
	return topicMatcher{
		exact: pattern,
		match: func(topic string) bool { return strings.TrimSpace(topic) == pattern },
	}, nil
}

func (m topicMatcher) matches(topic string) bool {
	if m.match == nil {
		return true
	}
	return m.match(strings.TrimSpace(topic))
}

func recordExtractionMetrics(tenant string, counts map[model.SafetyDecision]int) {
	for verdict, count := range counts {
		if count <= 0 {
			continue
		}
		wire, err := verdict.DecisionLogWireValue()
		if err != nil {
			wire = strings.ToLower(string(verdict))
		}
		extractionTotal.WithLabelValues(tenant, wire).Add(float64(count))
	}
}

type jobRequestCache struct {
	capacity int
	ll       *list.List
	entries  map[string]*list.Element
}

type jobRequestCacheEntry struct {
	jobID string
	req   any
}

func newJobRequestCache(capacity int) *jobRequestCache {
	if capacity <= 0 {
		capacity = 1
	}
	return &jobRequestCache{
		capacity: capacity,
		ll:       list.New(),
		entries:  make(map[string]*list.Element, capacity),
	}
}

func (c *jobRequestCache) getOrLoad(ctx context.Context, jobID string, store JobRequestStore) (any, error) {
	if store == nil {
		return nil, fmt.Errorf("job store is required")
	}
	if elem, ok := c.entries[jobID]; ok {
		c.ll.MoveToFront(elem)
		return elem.Value.(*jobRequestCacheEntry).req, nil
	}

	req, err := store.GetJobRequest(ctx, jobID)
	if err != nil {
		return nil, err
	}
	elem := c.ll.PushFront(&jobRequestCacheEntry{jobID: jobID, req: req})
	c.entries[jobID] = elem
	if c.ll.Len() > c.capacity {
		tail := c.ll.Back()
		if tail != nil {
			c.ll.Remove(tail)
			delete(c.entries, tail.Value.(*jobRequestCacheEntry).jobID)
		}
	}
	return req, nil
}
