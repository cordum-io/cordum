// Package calibration builds a calibration report from the Edge evidence
// store: the decisions recorded on Edge events of one tenant's observe-mode
// sessions, over a time window and a scope. No action input is copied into
// the report; every name taken from an event is redacted first.
package calibration

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/cordum/cordum/core/edge"
)

const (
	maxWindow    = 90 * 24 * time.Hour
	maxSessions  = 1000
	maxEvents    = 100000
	topN         = 10
	pageLimit    = 200
	redactedName = "[redacted]"
	dayLayout    = "2006-01-02"
)

// Source is the subset of edge.Store the report reads.
type Source interface {
	ListSessions(ctx context.Context, q edge.ListSessionsQuery) (edge.SessionPage, error)
	ListEvents(ctx context.Context, q edge.ListEventsQuery) (edge.EventPage, error)
}

var _ Source = edge.Store(nil)

// Request selects the sessions and events a report counts. Repos and Labels
// narrow the sessions when set; RuleIDs are the rules checked for never_fired.
type Request struct {
	TenantID     string
	Since, Until time.Time
	Repos        []string
	Labels       map[string]string
	RuleIDs      []string
}

// RuleCount is the number of deny and require-approval decisions recorded
// for one rule id.
type RuleCount struct {
	RuleID          string `json:"rule_id"`
	Deny            int    `json:"deny"`
	RequireApproval int    `json:"require_approval"`
}

// NameCount is one entry of a top list.
type NameCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// DayCount is the number of require-approval decisions on one UTC day.
type DayCount struct {
	Day   string `json:"day"`
	Count int    `json:"count"`
}

// Report is the calibration report. EventsScanned counts the distinct events
// of the window that were counted; Truncated is set when a scan cap stopped
// the scan early.
type Report struct {
	ID                string            `json:"id"`
	TenantID          string            `json:"tenant_id"`
	Since             time.Time         `json:"since"`
	Until             time.Time         `json:"until"`
	Repos             []string          `json:"repos,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
	SessionsObserved  int               `json:"sessions_observed"`
	EventsScanned     int               `json:"events_scanned"`
	Truncated         bool              `json:"truncated"`
	Rules             []RuleCount       `json:"rules"`
	NeverFired        []string          `json:"never_fired"`
	TopTools          []NameCount       `json:"top_tools"`
	TopCommandClasses []NameCount       `json:"top_command_classes"`
	ApprovalsPerDay   []DayCount        `json:"approvals_per_day"`
}

type aggregator struct {
	req       Request
	sessions  map[string]bool
	events    map[string]bool
	fired     map[string]bool
	rules     map[string]*RuleCount
	tools     map[string]int
	classes   map[string]int
	approvals map[string]int
	observed  int
	truncated bool
}

// Build counts the decisions recorded on req's events in [Since, Until) and
// returns the report with its content-derived id.
func Build(ctx context.Context, src Source, req Request) (Report, error) {
	req.TenantID = strings.TrimSpace(req.TenantID)
	switch {
	case req.TenantID == "":
		return Report{}, fmt.Errorf("%w: calibration tenant_id is required", edge.ErrValidation)
	case !req.Until.After(req.Since):
		return Report{}, fmt.Errorf("%w: calibration until must be after since", edge.ErrValidation)
	case req.Until.Sub(req.Since) > maxWindow:
		return Report{}, fmt.Errorf("%w: calibration window exceeds 90 days", edge.ErrValidation)
	}
	a := &aggregator{
		req: req, sessions: map[string]bool{}, events: map[string]bool{}, fired: map[string]bool{},
		rules: map[string]*RuleCount{}, tools: map[string]int{}, classes: map[string]int{}, approvals: map[string]int{},
	}
	if err := a.scanSessions(ctx, src); err != nil {
		return Report{}, err
	}
	rep := a.report()
	body, err := json.Marshal(rep)
	if err != nil {
		return Report{}, fmt.Errorf("calibration: marshal report: %w", err)
	}
	sum := sha256.Sum256(body)
	rep.ID = "cal-" + hex.EncodeToString(sum[:])[:32]
	return rep, nil
}

func (a *aggregator) scanSessions(ctx context.Context, src Source) error {
	cursor := ""
	for {
		page, err := src.ListSessions(ctx, edge.ListSessionsQuery{TenantID: a.req.TenantID, Cursor: cursor, Limit: pageLimit})
		if err != nil {
			return fmt.Errorf("calibration: list sessions: %w", err)
		}
		for _, s := range page.Items {
			if a.sessions[s.SessionID] {
				continue
			}
			if len(a.sessions) == maxSessions {
				a.truncated = true
				return nil
			}
			a.sessions[s.SessionID] = true
			if !a.inScope(s) {
				continue
			}
			if err := a.scanEvents(ctx, src, s.SessionID); err != nil || a.truncated {
				return err
			}
		}
		if page.NextCursor == "" {
			return nil
		}
		cursor = page.NextCursor
	}
}

func (a *aggregator) inScope(s edge.EdgeSession) bool {
	if s.TenantID != a.req.TenantID || s.PolicyMode != edge.PolicyModeObserve {
		return false
	}
	if !s.StartedAt.Before(a.req.Until) || (s.EndedAt != nil && s.EndedAt.Before(a.req.Since)) {
		return false
	}
	if len(a.req.Repos) > 0 && !slices.Contains(a.req.Repos, s.Repo) {
		return false
	}
	for k, v := range a.req.Labels {
		if got, ok := s.Labels[k]; !ok || got != v {
			return false
		}
	}
	return true
}

func (a *aggregator) scanEvents(ctx context.Context, src Source, sessionID string) error {
	before := len(a.events)
	defer func() {
		if len(a.events) > before {
			a.observed++
		}
	}()
	q := edge.ListEventsQuery{TenantID: a.req.TenantID, SessionID: sessionID, Since: a.req.Since, Until: a.req.Until, Limit: pageLimit}
	for {
		page, err := src.ListEvents(ctx, q)
		if err != nil {
			return fmt.Errorf("calibration: list events: %w", err)
		}
		for _, ev := range page.Items {
			// The store's Until is inclusive; the report's window is not.
			if ev.TenantID != a.req.TenantID || ev.Timestamp.Before(a.req.Since) || !ev.Timestamp.Before(a.req.Until) || a.events[ev.EventID] {
				continue
			}
			if len(a.events) == maxEvents {
				a.truncated = true
				return nil
			}
			a.events[ev.EventID] = true
			a.count(ev)
		}
		if page.NextCursor == "" {
			return nil
		}
		q.Cursor = page.NextCursor
	}
}

func (a *aggregator) count(ev edge.AgentActionEvent) {
	a.fired[ev.RuleID] = true
	if ev.Decision != edge.DecisionDeny && ev.Decision != edge.DecisionRequireApproval {
		return
	}
	rule := redactName(ev.RuleID)
	rc := a.rules[rule]
	if rc == nil {
		rc = &RuleCount{RuleID: rule}
		a.rules[rule] = rc
	}
	if ev.Decision == edge.DecisionDeny {
		rc.Deny++
	} else {
		rc.RequireApproval++
		a.approvals[ev.Timestamp.UTC().Format(dayLayout)]++
	}
	a.tools[redactName(ev.ToolName)]++
	a.classes[redactName(ev.ActionName)]++
}

// redactName returns name unchanged unless the Edge redactor finds a secret
// in it or fails, in which case it returns the placeholder.
func redactName(name string) string {
	res, err := edge.RedactValue(name, edge.RedactionOptions{HashMode: edge.RedactionHashNone})
	if err != nil || res.Redacted {
		return redactedName
	}
	if s, ok := res.Value.(string); ok {
		return s
	}
	return redactedName
}

func (a *aggregator) report() Report {
	rep := Report{
		TenantID: a.req.TenantID, Since: a.req.Since.UTC(), Until: a.req.Until.UTC(),
		Repos: a.req.Repos, Labels: a.req.Labels,
		SessionsObserved: a.observed, EventsScanned: len(a.events), Truncated: a.truncated,
		Rules: []RuleCount{}, NeverFired: []string{},
		TopTools: top(a.tools), TopCommandClasses: top(a.classes), ApprovalsPerDay: []DayCount{},
	}
	for _, rc := range a.rules {
		rep.Rules = append(rep.Rules, *rc)
	}
	slices.SortFunc(rep.Rules, func(x, y RuleCount) int { return strings.Compare(x.RuleID, y.RuleID) })
	ids := slices.Clone(a.req.RuleIDs)
	slices.Sort(ids)
	for _, id := range slices.Compact(ids) {
		if !a.fired[id] {
			rep.NeverFired = append(rep.NeverFired, id)
		}
	}
	since := a.req.Since.UTC()
	last := a.req.Until.Add(-time.Nanosecond).UTC()
	for d := time.Date(since.Year(), since.Month(), since.Day(), 0, 0, 0, 0, time.UTC); !d.After(last); d = d.AddDate(0, 0, 1) {
		day := d.Format(dayLayout)
		rep.ApprovalsPerDay = append(rep.ApprovalsPerDay, DayCount{Day: day, Count: a.approvals[day]})
	}
	return rep
}

func top(counts map[string]int) []NameCount {
	out := make([]NameCount, 0, len(counts))
	for name, n := range counts {
		out = append(out, NameCount{Name: name, Count: n})
	}
	slices.SortFunc(out, func(x, y NameCount) int {
		return cmp.Or(cmp.Compare(y.Count, x.Count), strings.Compare(x.Name, y.Name))
	})
	return out[:min(len(out), topN)]
}
