package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"testing"
)

func requireReadOnlyUnsupported(t *testing.T, name string, err error) {
	t.Helper()
	var berr *BridgeError
	if !errors.As(err, &berr) || berr.StatusCode != http.StatusNotImplemented || berr.Code != "read_only_unsupported" {
		t.Fatalf("%s error = %v, want 501 read_only_unsupported", name, err)
	}
}

// TestDirectBridge_ReadHooks pins DirectServiceBridge's read hooks. Without a
// hook each read still answers 501 read_only_unsupported. With one, GetJob
// trims the ID, refuses a blank one before the hook runs, and both GetJob and
// GetStatus return the hook's item or error unchanged. The nine GetJSONFunc
// reads call the hook with HTTPServiceBridge's route and query and reshape
// its answer; ListPacks, ListWorkers and QueryAudit stay 501.
func TestDirectBridge_ReadHooks(t *testing.T) {
	ctx := context.Background()

	t.Run("no hooks answer 501", func(t *testing.T) {
		for _, b := range []*DirectServiceBridge{nil, NewDirectServiceBridge(DirectServiceBridgeConfig{})} {
			item, err := b.GetJob(ctx, "job-1")
			if item != nil {
				t.Fatalf("GetJob item = %+v, want nil", item)
			}
			requireReadOnlyUnsupported(t, "GetJob", err)
			item, err = b.GetStatus(ctx)
			if item != nil {
				t.Fatalf("GetStatus item = %+v, want nil", item)
			}
			requireReadOnlyUnsupported(t, "GetStatus", err)
			for _, r := range directJSONReads(ctx) {
				_, err := r.call(b)
				requireReadOnlyUnsupported(t, r.name, err)
			}
		}
	})

	var gotIDs []string
	hookErr := NewBridgeError(http.StatusForbidden, "forbidden", "tenant access denied", nil)
	b := NewDirectServiceBridge(DirectServiceBridgeConfig{
		GetJobFunc: func(_ context.Context, jobID string) (*ResourceItem, error) {
			gotIDs = append(gotIDs, jobID)
			if jobID == "job-denied" {
				return nil, hookErr
			}
			return &ResourceItem{ID: jobID, Kind: "job", Data: map[string]any{"id": jobID}}, nil
		},
		GetStatusFunc: func(context.Context) (*ResourceItem, error) {
			return &ResourceItem{Kind: "status", Data: map[string]any{"uptime_seconds": 7}}, nil
		},
	})

	t.Run("GetJob trims and delegates", func(t *testing.T) {
		item, err := b.GetJob(ctx, "  job-1 ")
		if err != nil || item == nil || item.ID != "job-1" || item.Kind != "job" || item.Data["id"] != "job-1" {
			t.Fatalf("GetJob = %+v, %v; want job-1", item, err)
		}
	})
	t.Run("GetJob refuses a blank id before the hook", func(t *testing.T) {
		before := len(gotIDs)
		for _, id := range []string{"", "   "} {
			item, err := b.GetJob(ctx, id)
			if item != nil || !errors.Is(err, ErrInvalidParams) {
				t.Fatalf("GetJob(%q) = %+v, %v; want ErrInvalidParams", id, item, err)
			}
		}
		if len(gotIDs) != before {
			t.Fatalf("hook ran for a blank id: %v", gotIDs[before:])
		}
	})
	t.Run("GetJob returns the hook error unchanged", func(t *testing.T) {
		item, err := b.GetJob(ctx, "job-denied")
		if item != nil || err != hookErr {
			t.Fatalf("GetJob(job-denied) = %+v, %v; want nil and the hook's error", item, err)
		}
	})
	t.Run("GetStatus delegates", func(t *testing.T) {
		item, err := b.GetStatus(ctx)
		if err != nil || item == nil || item.Kind != "status" || item.Data["uptime_seconds"] != 7 {
			t.Fatalf("GetStatus = %+v, %v; want the hook's status item", item, err)
		}
	})
	if want := []string{"job-1", "job-denied"}; len(gotIDs) != len(want) || gotIDs[0] != want[0] || gotIDs[1] != want[1] {
		t.Fatalf("hook ids = %v, want %v", gotIDs, want)
	}

	t.Run("GetJSONFunc", func(t *testing.T) { testDirectJSONReads(t, ctx) })
}

func testDirectJSONReads(t *testing.T, ctx context.Context) {
	var calls []string
	answer := ""
	hookErr := NewBridgeError(http.StatusForbidden, "forbidden", "tenant access denied", nil)
	b := NewDirectServiceBridge(DirectServiceBridgeConfig{
		GetJSONFunc: func(_ context.Context, path string, query url.Values, out any) error {
			calls = append(calls, path+"?"+query.Encode())
			if answer == "deny" {
				return hookErr
			}
			return json.Unmarshal([]byte(answer), out)
		},
	})
	for _, r := range directJSONReads(ctx) {
		calls, answer = nil, r.answer
		got, err := r.call(b)
		if err != nil || !r.ok(got) {
			t.Fatalf("%s = %+v, %v; want the hook's answer reshaped", r.name, got, err)
		}
		if want := []string{r.path + "?" + r.query}; len(calls) != 1 || calls[0] != want[0] {
			t.Fatalf("%s hook calls = %q, want %q", r.name, calls, want)
		}
	}

	answer = "deny"
	if item, err := b.VerifyAudit(ctx, "tenant-b"); item != nil || err != hookErr {
		t.Fatalf("VerifyAudit(tenant-b) = %+v, %v; want nil and the hook's error", item, err)
	}
	answer = "null"
	if page, err := b.ListJobs(ctx, ListInput{}); err != nil || page == nil || len(page.Items) != 0 {
		t.Fatalf("ListJobs(null answer) = %+v, %v; want an empty page", page, err)
	}
	answer = `"not an object"`
	if page, err := b.ListJobs(ctx, ListInput{}); page != nil || err == nil {
		t.Fatalf("ListJobs(string answer) = %+v, %v; want an error", page, err)
	}

	calls = nil
	_, err := b.ListPacks(ctx, ListInput{})
	requireReadOnlyUnsupported(t, "ListPacks", err)
	_, err = b.ListWorkers(ctx, ListInput{})
	requireReadOnlyUnsupported(t, "ListWorkers", err)
	_, err = b.QueryAudit(ctx, AuditQueryInput{})
	requireReadOnlyUnsupported(t, "QueryAudit", err)
	if len(calls) != 0 {
		t.Fatalf("hook ran for an unsupported read: %q", calls)
	}
}

type directJSONRead struct {
	name, path, query, answer string
	call                      func(*DirectServiceBridge) (any, error)
	ok                        func(any) bool
}

// directJSONReads lists the nine GetJSONFunc reads with the route and query
// HTTPServiceBridge sends for each, the JSON the hook answers and a check on
// the reshaped result.
func directJSONReads(ctx context.Context) []directJSONRead {
	in := ListInput{PageSize: 5, Cursor: "c1"}
	const page, q = `{"items":[{"id":"x"}],"next_cursor":"n1"}`, "cursor=c1&limit=5"
	list := func(name, path, query, answer, cursor string, read func(*DirectServiceBridge, context.Context, ListInput) (*ListPage, error)) directJSONRead {
		return directJSONRead{name: name, path: path, query: query, answer: answer,
			call: func(b *DirectServiceBridge) (any, error) { return read(b, ctx, in) },
			ok: func(got any) bool {
				p, _ := got.(*ListPage)
				return p != nil && len(p.Items) == 1 && p.Items[0]["id"] == "x" && p.NextCursor == cursor
			}}
	}
	item := func(name, path, query, answer, id, kind, key string, read func(*DirectServiceBridge) (*ResourceItem, error)) directJSONRead {
		return directJSONRead{name: name, path: path, query: query, answer: answer,
			call: func(b *DirectServiceBridge) (any, error) { return read(b) },
			ok: func(got any) bool {
				it, _ := got.(*ResourceItem)
				return it != nil && it.ID == id && it.Kind == kind && it.Data[key] != nil
			}}
	}
	return []directJSONRead{
		list("ListJobs", "/api/v1/jobs", q, page, "n1", (*DirectServiceBridge).ListJobs),
		list("ListRuns", "/api/v1/workflow-runs", q, page, "n1", (*DirectServiceBridge).ListRuns),
		item("GetRun", "/api/v1/workflow-runs/run-1", "", `{"id":"run-1"}`, "run-1", "run", "id",
			func(b *DirectServiceBridge) (*ResourceItem, error) { return b.GetRun(ctx, " run-1 ") }),
		item("GetRunTimeline", "/api/v1/workflow-runs/run%2F1/timeline", "", `[{"type":"step"}]`, "run/1", "run_timeline", "events",
			func(b *DirectServiceBridge) (*ResourceItem, error) { return b.GetRunTimeline(ctx, "run/1") }),
		list("ListWorkflows", "/api/v1/workflows", q, `[{"id":"x"}]`, "", (*DirectServiceBridge).ListWorkflows),
		list("ListTopics", "/api/v1/topics", q, page, "n1", (*DirectServiceBridge).ListTopics),
		list("ListAgents", "/api/v1/agents", q, page, "n1", (*DirectServiceBridge).ListAgents),
		list("ListPendingApprovals", "/api/v1/approvals", q+"&status=pending", page, "n1", (*DirectServiceBridge).ListPendingApprovals),
		item("VerifyAudit", "/api/v1/audit/verify", "tenant=tenant-a", `{"ok":true}`, "", "audit_verify", "ok",
			func(b *DirectServiceBridge) (*ResourceItem, error) { return b.VerifyAudit(ctx, "tenant-a") }),
	}
}
