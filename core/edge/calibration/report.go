// Package calibration builds a calibration report from the Edge evidence
// store: the decisions recorded on Edge events of one tenant's observe-mode
// sessions, over a time window and a scope, what candidate shadow bundles
// would have decided, and the assurance state of the observed sessions. No
// action input is copied into the report; every name is redacted first.
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
	HeartbeatAlive(ctx context.Context, tenantID, sessionID string) (bool, error)
}

var _ Source = edge.Store(nil)

// ShadowVerdict is one decision a candidate shadow bundle would have made.
type ShadowVerdict struct {
	BundleID, RuleID, Verdict string
	At                        time.Time
}

// Request selects the sessions and events a report counts. Repos and Labels
// narrow the sessions when set; RuleIDs are the rules checked for never_fired.
// Shadow holds the tenant's shadow verdicts; ShadowTruncated marks a shadow
// scan that a cap stopped early.
type Request struct {
	TenantID        string
	Since, Until    time.Time
	Repos           []string
	Labels          map[string]string
	RuleIDs         []string
	Shadow          []ShadowVerdict
	ShadowTruncated bool
}

// RuleCount is the number of deny and require-approval decisions recorded
// for one rule id.
type RuleCount struct {
	RuleID          string `json:"rule_id"`
	Deny            int    `json:"deny"`
	RequireApproval int    `json:"require_approval"`
}

// ShadowRuleCount is the number of deny and require-approval decisions a
// candidate shadow bundle would have made for one rule id.
type ShadowRuleCount struct {
	BundleID        string `json:"bundle_id"`
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
// of the window that were counted; Truncated is set when a scan cap, the
// caller's shadow scan included, stopped a scan early.
//
// ShadowRules covers the whole tenant and window: shadow results carry no
// session, repository or label, so Repos and Labels do not narrow it.
// Assurance maps every assurance state to the number of observed sessions in
// it, derived when the report is built from each session's facts and current
// heartbeat.
//
// TopCommandClasses counts the command.class label that the Edge classifier
// puts on shell commands. An event without one, such as a file or MCP tool
// call, counts in TopTools only.
type Report struct {
	ID                string                      `json:"id"`
	TenantID          string                      `json:"tenant_id"`
	Since             time.Time                   `json:"since"`
	Until             time.Time                   `json:"until"`
	Repos             []string                    `json:"repos,omitempty"`
	Labels            map[string]string           `json:"labels,omitempty"`
	SessionsObserved  int                         `json:"sessions_observed"`
	EventsScanned     int                         `json:"events_scanned"`
	Truncated         bool                        `json:"truncated"`
	Rules             []RuleCount                 `json:"rules"`
	NeverFired        []string                    `json:"never_fired"`
	TopTools          []NameCount                 `json:"top_tools"`
	TopCommandClasses []NameCount                 `json:"top_command_classes"`
	ApprovalsPerDay   []DayCount                  `json:"approvals_per_day"`
	ShadowRules       []ShadowRuleCount           `json:"shadow_rules"`
	Assurance         map[edge.AssuranceState]int `json:"assurance"`
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
	assurance map[edge.AssuranceState]int
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
		assurance: map[edge.AssuranceState]int{
			edge.AssuranceStateCompatibility: 0, edge.AssuranceStateHookMediated: 0, edge.AssuranceStateExecutorEnforced: 0,
			edge.AssuranceStateUnsupported: 0, edge.AssuranceStateStale: 0,
		},
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
			before := len(a.events)
			err := a.scanEvents(ctx, src, s.SessionID)
			if err == nil && len(a.events) > before {
				err = a.observe(ctx, src, s)
			}
			if err != nil || a.truncated {
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

// observe counts s, a session with at least one counted event, and its
// assurance state.
func (a *aggregator) observe(ctx context.Context, src Source, s edge.EdgeSession) error {
	alive, err := src.HeartbeatAlive(ctx, a.req.TenantID, s.SessionID)
	if err != nil {
		return fmt.Errorf("calibration: session heartbeat: %w", err)
	}
	a.observed++
	a.assurance[edge.DeriveAssuranceState(s, alive)]++
	return nil
}

func (a *aggregator) scanEvents(ctx context.Context, src Source, sessionID string) error {
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
	// The class is the classifier's label. ActionName is bash.exec for every
	// shell command, and agentd evidence names its decision there instead.
	if class := strings.TrimSpace(ev.Labels["command.class"]); class != "" {
		a.classes[redactName(class)]++
	}
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
		SessionsObserved: a.observed, EventsScanned: len(a.events), Truncated: a.truncated || a.req.ShadowTruncated,
		Rules: []RuleCount{}, NeverFired: []string{},
		TopTools: top(a.tools), TopCommandClasses: top(a.classes), ApprovalsPerDay: []DayCount{},
		ShadowRules: shadowRules(a.req), Assurance: a.assurance,
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

// shadowRules counts the deny and require-approval verdicts in req.Shadow
// with At in [Since, Until), per bundle and rule, matching verdicts
// case-insensitively.
func shadowRules(req Request) []ShadowRuleCount {
	counts := map[[2]string]*ShadowRuleCount{}
	for _, v := range req.Shadow {
		deny := strings.EqualFold(v.Verdict, string(edge.DecisionDeny))
		if v.At.Before(req.Since) || !v.At.Before(req.Until) ||
			(!deny && !strings.EqualFold(v.Verdict, string(edge.DecisionRequireApproval))) {
			continue
		}
		key := [2]string{redactName(v.BundleID), redactName(v.RuleID)}
		sc := counts[key]
		if sc == nil {
			sc = &ShadowRuleCount{BundleID: key[0], RuleID: key[1]}
			counts[key] = sc
		}
		if deny {
			sc.Deny++
		} else {
			sc.RequireApproval++
		}
	}
	out := make([]ShadowRuleCount, 0, len(counts))
	for _, sc := range counts {
		out = append(out, *sc)
	}
	slices.SortFunc(out, func(x, y ShadowRuleCount) int {
		return cmp.Or(strings.Compare(x.BundleID, y.BundleID), strings.Compare(x.RuleID, y.RuleID))
	})
	return out
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
