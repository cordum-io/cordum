package calibration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/edge"
)

// fakeSource mimics the Redis store's list semantics: pages through a
// numeric cursor, filters by tenant (unless ignoreTenant), session and an
// inclusive [Since, Until]. A heartbeat is alive only for sessions in alive.
type fakeSource struct {
	sessions     []edge.EdgeSession
	events       []edge.AgentActionEvent
	alive        map[string]bool
	aliveErr     error
	ignoreTenant bool
	calls        int
}

func page[T any](items []T, cursor string, limit int) ([]T, string) {
	start, _ := strconv.Atoi(cursor)
	end := min(start+limit, len(items))
	if end < len(items) {
		return items[start:end], strconv.Itoa(end)
	}
	return items[start:end], ""
}

func (f *fakeSource) ListSessions(_ context.Context, q edge.ListSessionsQuery) (edge.SessionPage, error) {
	f.calls++
	var items []edge.EdgeSession
	for _, s := range f.sessions {
		if f.ignoreTenant || s.TenantID == q.TenantID {
			items = append(items, s)
		}
	}
	out, next := page(items, q.Cursor, q.Limit)
	return edge.SessionPage{Items: out, NextCursor: next}, nil
}

func (f *fakeSource) ListEvents(_ context.Context, q edge.ListEventsQuery) (edge.EventPage, error) {
	f.calls++
	var items []edge.AgentActionEvent
	for _, e := range f.events {
		if e.SessionID != q.SessionID || (!f.ignoreTenant && e.TenantID != q.TenantID) {
			continue
		}
		if e.Timestamp.Before(q.Since) || e.Timestamp.After(q.Until) {
			continue
		}
		items = append(items, e)
	}
	out, next := page(items, q.Cursor, q.Limit)
	return edge.EventPage{Items: out, NextCursor: next}, nil
}

func (f *fakeSource) HeartbeatAlive(_ context.Context, _, sessionID string) (bool, error) {
	f.calls++
	if f.aliveErr != nil {
		return false, f.aliveErr
	}
	return f.alive[sessionID], nil
}

var (
	since = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	until = time.Date(2026, 3, 3, 12, 0, 0, 0, time.UTC)
)

func at(day, hour int) time.Time { return time.Date(2026, 3, day, hour, 0, 0, 0, time.UTC) }

func observe(id, repo string, labels edge.Labels) edge.EdgeSession {
	return edge.EdgeSession{SessionID: id, TenantID: "t1", Repo: repo, PolicyMode: edge.PolicyModeObserve, Labels: labels, StartedAt: at(1, 10)}
}

// event builds an Edge event whose command.class label is class, with no
// label when class is empty. Every event gets the action name the classifier
// gives a shell command, so the action name cannot stand in for the class.
func event(id, sessionID string, ts time.Time, d edge.EdgeDecision, rule, tool, class string) edge.AgentActionEvent {
	ev := edge.AgentActionEvent{EventID: id, SessionID: sessionID, TenantID: "t1", Timestamp: ts, Decision: d, RuleID: rule, ToolName: tool, ActionName: "bash.exec"}
	if class != "" {
		ev.Labels = edge.Labels{"command.class": class}
	}
	return ev
}

func build(t *testing.T, src Source, req Request) Report {
	t.Helper()
	rep, err := Build(context.Background(), src, req)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return rep
}

func TestCalibrationBuildCountsSeededDataset(t *testing.T) {
	agentd := event("e5", "s2", at(3, 11), edge.DecisionRequireApproval, "r.approve-push", "Bash", "deploy")
	agentd.ActionName = "claude.pretooluse.decision" // agentd evidence names the decision
	src := &fakeSource{
		sessions: []edge.EdgeSession{observe("s1", "org/api", nil), observe("s2", "org/web", nil)},
		events: []edge.AgentActionEvent{
			event("e1", "s1", at(1, 13), edge.DecisionDeny, "r.deny-rm", "Bash", "destructive"),
			event("e2", "s1", at(1, 14), edge.DecisionRequireApproval, "r.approve-push", "Bash", "deploy"),
			event("e3", "s1", at(1, 15), edge.DecisionAllow, "r.allow-ls", "Bash", "safe"),
			event("e1", "s1", at(1, 13), edge.DecisionDeny, "r.deny-rm", "Bash", "destructive"), // duplicate delivery
			event("e4", "s1", at(2, 9), edge.DecisionDeny, "", "Write", ""),                     // no command class
			agentd,
			event("e6", "s2", until, edge.DecisionDeny, "r.deny-rm", "Bash", "destructive"), // exactly Until
			event("e7", "s2", at(2, 10), edge.DecisionDeny, "r.deny-rm", "Edit", " "),       // blank command class
		},
	}
	got := build(t, src, Request{TenantID: "t1", Since: since, Until: until})
	got.ID = ""
	want := Report{
		TenantID: "t1", Since: since, Until: until,
		SessionsObserved: 2, EventsScanned: 6,
		Rules: []RuleCount{
			{RuleID: "", Deny: 1},
			{RuleID: "r.approve-push", RequireApproval: 2},
			{RuleID: "r.deny-rm", Deny: 2},
		},
		NeverFired:        []string{},
		TopTools:          []NameCount{{"Bash", 3}, {"Edit", 1}, {"Write", 1}},
		TopCommandClasses: []NameCount{{"deploy", 2}, {"destructive", 1}},
		ApprovalsPerDay:   []DayCount{{"2026-03-01", 1}, {"2026-03-02", 0}, {"2026-03-03", 1}},
		ShadowRules:       []ShadowRuleCount{},
		Assurance:         map[edge.AssuranceState]int{"compatibility": 0, "hook-mediated": 0, "executor-enforced": 0, "unsupported": 0, "stale": 2},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("report mismatch\n got: %+v\nwant: %+v", got, want)
	}
}

func TestCalibrationBuildRedactsSeededSecret(t *testing.T) {
	const secret = "AKIAIOSFODNN7EXAMPLE"
	ev := event("e1", "s1", at(2, 9), edge.DecisionDeny, "rule-"+secret, secret, "aws "+secret)
	ev.InputRedacted = map[string]any{"command": "export KEY=" + secret}
	ev.DecisionReason = "matched key " + secret
	src := &fakeSource{sessions: []edge.EdgeSession{observe("s1", "org/api", nil)}, events: []edge.AgentActionEvent{ev}}
	body, err := json.Marshal(build(t, src, Request{TenantID: "t1", Since: since, Until: until}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(body), secret) {
		t.Fatalf("report leaks the seeded secret: %s", body)
	}
	if !strings.Contains(string(body), `"[redacted]"`) {
		t.Fatalf("report has no [redacted] placeholder: %s", body)
	}
}

func TestCalibrationBuildFiltersTenantAndScope(t *testing.T) {
	scope := edge.Labels{"env": "pilot"}
	foreign := observe("s-tenant", "org/api", scope)
	foreign.TenantID = "t2"
	enforce := observe("s-enforce", "org/api", scope)
	enforce.PolicyMode = edge.PolicyModeEnforce
	foreignEvent := event("e-tenant-event", "s-in", at(2, 9), edge.DecisionDeny, "r.tenant-event", "T2Tool", "t2-class")
	foreignEvent.TenantID = "t2"
	src := &fakeSource{
		ignoreTenant: true, // the package must re-check the tenant itself
		sessions: []edge.EdgeSession{
			observe("s-in", "org/api", edge.Labels{"env": "pilot", "team": "core"}),
			foreign, enforce,
			observe("s-repo", "org/other", scope),
			observe("s-label", "org/api", edge.Labels{"env": "prod"}),
			observe("s-nolabels", "org/api", nil),
		},
		events: []edge.AgentActionEvent{
			event("e-in", "s-in", at(2, 8), edge.DecisionDeny, "r.in", "Bash", "rm"),
			foreignEvent,
			// stamped t1, so only the session's own tenant keeps it out
			event("e-tenant", "s-tenant", at(2, 9), edge.DecisionDeny, "r.tenant", "TenantTool", "tenant-class"),
			event("e-enforce", "s-enforce", at(2, 9), edge.DecisionDeny, "r.enforce", "EnforceTool", "enforce-class"),
			event("e-repo", "s-repo", at(2, 9), edge.DecisionDeny, "r.repo", "RepoTool", "repo-class"),
			event("e-label", "s-label", at(2, 9), edge.DecisionDeny, "r.label", "LabelTool", "label-class"),
			event("e-nolabels", "s-nolabels", at(2, 9), edge.DecisionDeny, "r.nolabels", "NoLabelTool", "nolabel-class"),
		},
	}
	got := build(t, src, Request{TenantID: "t1", Since: since, Until: until, Repos: []string{"org/api"}, Labels: map[string]string{"env": "pilot"}})
	if got.SessionsObserved != 1 || got.EventsScanned != 1 {
		t.Fatalf("sessions_observed=%d events_scanned=%d, want 1 and 1", got.SessionsObserved, got.EventsScanned)
	}
	checks := []struct {
		name      string
		got, want any
	}{
		{"rules", got.Rules, []RuleCount{{RuleID: "r.in", Deny: 1}}},
		{"top_tools", got.TopTools, []NameCount{{"Bash", 1}}},
		{"top_command_classes", got.TopCommandClasses, []NameCount{{"rm", 1}}},
	}
	for _, c := range checks {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s = %+v, want %+v", c.name, c.got, c.want)
		}
	}
}

func TestCalibrationBuildListsNeverFiredRules(t *testing.T) {
	src := &fakeSource{
		sessions: []edge.EdgeSession{observe("s1", "org/api", nil)},
		events: []edge.AgentActionEvent{
			event("e1", "s1", at(2, 8), edge.DecisionDeny, "a", "Bash", "rm"),
			event("e2", "s1", at(2, 9), edge.DecisionAllow, "b", "Bash", "ls"),
			event("e3", "s1", at(3, 13), edge.DecisionDeny, "c", "Bash", "rm"), // after Until
		},
	}
	got := build(t, src, Request{TenantID: "t1", Since: since, Until: until, RuleIDs: []string{"c", "a", "b"}})
	if !reflect.DeepEqual(got.NeverFired, []string{"c"}) {
		t.Fatalf("never_fired = %v, want [c]", got.NeverFired)
	}
	if !reflect.DeepEqual(got.Rules, []RuleCount{{RuleID: "a", Deny: 1}}) {
		t.Fatalf("rules = %+v, want [{a 1 0}]", got.Rules)
	}
}

func TestCalibrationReportIDStable(t *testing.T) {
	newSrc := func(d edge.EdgeDecision) *fakeSource {
		return &fakeSource{
			sessions: []edge.EdgeSession{observe("s1", "org/api", edge.Labels{"a": "1", "b": "2", "c": "3", "d": "4"})},
			events:   []edge.AgentActionEvent{event("e1", "s1", at(2, 8), d, "r1", "Bash", "rm")},
		}
	}
	req := Request{TenantID: "t1", Since: since, Until: until, Labels: map[string]string{"d": "4", "c": "3", "b": "2", "a": "1"}}
	first := build(t, newSrc(edge.DecisionDeny), req)
	second := build(t, newSrc(edge.DecisionDeny), req)
	if !regexp.MustCompile(`^cal-[0-9a-f]{32}$`).MatchString(first.ID) {
		t.Fatalf("id %q does not match ^cal-[0-9a-f]{32}$", first.ID)
	}
	if first.ID != second.ID {
		t.Fatalf("same input gave ids %q and %q", first.ID, second.ID)
	}
	body := first
	body.ID = ""
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	sum := sha256.Sum256(raw)
	if want := "cal-" + hex.EncodeToString(sum[:])[:32]; first.ID != want {
		t.Fatalf("id %q, want %q from the report body", first.ID, want)
	}
	if changed := build(t, newSrc(edge.DecisionRequireApproval), req); changed.ID == first.ID {
		t.Fatalf("a changed decision kept id %q", first.ID)
	}
}

func TestCalibrationBuildRejectsBadWindow(t *testing.T) {
	cases := []struct {
		name    string
		req     Request
		wantErr bool
	}{
		{"empty tenant", Request{TenantID: "  ", Since: since, Until: until}, true},
		{"until equals since", Request{TenantID: "t1", Since: since, Until: since}, true},
		{"until before since", Request{TenantID: "t1", Since: until, Until: since}, true},
		{"91-day window", Request{TenantID: "t1", Since: since, Until: since.AddDate(0, 0, 91)}, true},
		{"90-day window", Request{TenantID: "t1", Since: since, Until: since.AddDate(0, 0, 90)}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := &fakeSource{}
			_, err := Build(context.Background(), src, c.req)
			if !c.wantErr {
				if err != nil {
					t.Fatalf("Build: %v", err)
				}
				return
			}
			if !errors.Is(err, edge.ErrValidation) {
				t.Fatalf("err = %v, want edge.ErrValidation", err)
			}
			if src.calls != 0 {
				t.Fatalf("rejected request still read the store %d times", src.calls)
			}
		})
	}
}

func TestCalibrationBuildTruncatesAtSessionCap(t *testing.T) {
	for _, n := range []int{1000, 1001} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			src := &fakeSource{}
			for i := range n {
				id := fmt.Sprintf("s%04d", i)
				src.sessions = append(src.sessions, observe(id, "org/api", nil))
				src.events = append(src.events, event("e-"+id, id, at(2, 8), edge.DecisionAllow, "r", "Bash", "ls"))
			}
			got := build(t, src, Request{TenantID: "t1", Since: since, Until: until})
			if got.Truncated != (n > 1000) || got.SessionsObserved != 1000 || got.EventsScanned != 1000 {
				t.Fatalf("n=%d: truncated=%v sessions_observed=%d events_scanned=%d, want %v 1000 1000",
					n, got.Truncated, got.SessionsObserved, got.EventsScanned, n > 1000)
			}
		})
	}
}

func TestCalibrationShadowRulesCounts(t *testing.T) {
	const secret = "AKIAIOSFODNN7EXAMPLE"
	req := Request{TenantID: "t1", Since: since, Until: until, Repos: []string{"org/none"}, Shadow: []ShadowVerdict{
		{"b1", "r.deny", "deny", since}, // exactly Since: counted
		{"b1", "r.deny", "DENY", at(2, 9)},
		{"b1", "r.approve", "require_approval", at(2, 10)},
		{"b1", "r.deny", "deny", until},                   // exactly Until: excluded
		{"b1", "r.deny", "deny", since.Add(-time.Second)}, // before Since: excluded
		{"b1", "r.allow", "allow", at(2, 11)},             // allow: not counted
		{"b2", "r.deny", "deny", at(3, 11)},
		{"b2", "r.approve", "REQUIRE_APPROVAL", at(1, 13)},
		{"b2", "r.approve", "require_approval", at(2, 13)},
		{"b2", secret, "deny", at(2, 12)},
	}}
	got := build(t, &fakeSource{}, req)
	want := []ShadowRuleCount{
		{"b1", "r.approve", 0, 1},
		{"b1", "r.deny", 2, 0},
		{"b2", "[redacted]", 1, 0},
		{"b2", "r.approve", 0, 2},
		{"b2", "r.deny", 1, 0},
	}
	if !reflect.DeepEqual(got.ShadowRules, want) {
		t.Fatalf("shadow_rules = %+v, want %+v", got.ShadowRules, want)
	}
	if got.Truncated {
		t.Fatalf("truncated = true without a capped scan")
	}
	req.ShadowTruncated = true
	if capped := build(t, &fakeSource{}, req); !capped.Truncated || capped.ID == got.ID {
		t.Fatalf("ShadowTruncated gave truncated=%v id %q, want true and an id other than %q", capped.Truncated, capped.ID, got.ID)
	}
}

func TestCalibrationAssuranceCounts(t *testing.T) {
	hook := edge.EnforcementLayers{"hook": true}
	compat := observe("s-compat", "org/api", nil)
	compat.EnforcementLayers = hook
	endedAt := at(2, 12)
	ended := observe("s-ended", "org/api", nil)
	ended.EnforcementLayers, ended.Status, ended.EndedAt = hook, edge.SessionStatusEnded, &endedAt
	lost := observe("s-lost", "org/api", nil) // running, heartbeat not alive
	lost.EnforcementLayers, lost.Status = hook, edge.SessionStatusRunning
	src := &fakeSource{
		sessions: []edge.EdgeSession{compat, ended, lost, observe("s-idle", "org/api", nil)},
		events: []edge.AgentActionEvent{
			event("e1", "s-compat", at(2, 8), edge.DecisionAllow, "r", "Bash", "ls"),
			event("e2", "s-ended", at(2, 9), edge.DecisionAllow, "r", "Bash", "ls"),
			event("e3", "s-lost", at(2, 10), edge.DecisionAllow, "r", "Bash", "ls"),
		},
		// s-idle has no counted event, so it is not observed and not counted.
		alive: map[string]bool{"s-compat": true, "s-ended": true, "s-idle": true},
	}
	req := Request{TenantID: "t1", Since: since, Until: until}
	got := build(t, src, req)
	want := map[edge.AssuranceState]int{"compatibility": 1, "hook-mediated": 0, "executor-enforced": 0, "unsupported": 0, "stale": 2}
	if !reflect.DeepEqual(got.Assurance, want) {
		t.Fatalf("assurance = %v, want %v", got.Assurance, want)
	}
	src.aliveErr = errors.New("heartbeat store down")
	if _, err := Build(context.Background(), src, req); !errors.Is(err, src.aliveErr) {
		t.Fatalf("Build with a HeartbeatAlive error = %v, want it wrapped", err)
	}
}
