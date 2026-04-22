package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestParseCordumURI(t *testing.T) {
	cases := []struct {
		in      string
		want    cordumURI
		wantErr bool
	}{
		{"cordum://jobs/job-1", cordumURI{Kind: "jobs", ID: "job-1"}, false},
		{"cordum://runs/run-42", cordumURI{Kind: "runs", ID: "run-42"}, false},
		{"cordum://runs/run-42/timeline", cordumURI{Kind: "runs", ID: "run-42", SubResource: "timeline"}, false},
		{"cordum://workflows/wf-1", cordumURI{Kind: "workflows", ID: "wf-1"}, false},
		{"cordum://packs/pack-1", cordumURI{Kind: "packs", ID: "pack-1"}, false},
		{"cordum://topics/job.default", cordumURI{Kind: "topics", ID: "job.default"}, false},
		{"cordum://agents/agent-alpha", cordumURI{Kind: "agents", ID: "agent-alpha"}, false},
		{"cordum://audit/tenant-a/42", cordumURI{Kind: "audit", ID: "tenant-a", Extra: "42"}, false},
		{"http://jobs/x", cordumURI{}, true},
		{"cordum://", cordumURI{}, true},
		{"cordum://jobs", cordumURI{}, true},
		{"cordum://jobs/x/extra/bits", cordumURI{}, true},
	}
	for _, c := range cases {
		got, err := parseCordumURI(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parse(%q) expected error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parse(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parse(%q) = %+v want %+v", c.in, got, c.want)
		}
	}
}

func TestCordumResourceHandler_JobKindRoundtrip(t *testing.T) {
	bridge := &mockServiceBridge{}
	// Inject a GetJob override via a package-local mock to keep this
	// test independent of the DirectServiceBridge 501 stubs.
	bridge.getJob = func(ctx context.Context, id string) (*ResourceItem, error) {
		return &ResourceItem{ID: id, Kind: "job", Data: map[string]any{"id": id, "state": "done"}}, nil
	}
	handler := cordumResourceHandler(bridge, "jobs")
	got, err := handler(context.Background(), "cordum://jobs/abc-123")
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if got.URI != "cordum://jobs/abc-123" || got.MIMEType != "application/json" {
		t.Errorf("unexpected contents: %+v", got)
	}
	var item ResourceItem
	if err := json.Unmarshal([]byte(got.Text), &item); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if item.ID != "abc-123" {
		t.Errorf("id = %q", item.ID)
	}
}

func TestCordumResourceHandler_RejectsWrongKind(t *testing.T) {
	handler := cordumResourceHandler(&mockServiceBridge{}, "jobs")
	_, err := handler(context.Background(), "cordum://runs/xyz")
	if err == nil || !errors.Is(err, ErrInvalidCordumURI) {
		t.Errorf("want ErrInvalidCordumURI, got %v", err)
	}
}

func TestCordumResourceHandler_RejectsMalformed(t *testing.T) {
	handler := cordumResourceHandler(&mockServiceBridge{}, "jobs")
	_, err := handler(context.Background(), "not-a-uri")
	if err == nil {
		t.Error("want error")
	}
}

func TestRegisterCordumURIResources_RegistersEight(t *testing.T) {
	reg := NewResourceRegistry()
	if err := RegisterCordumURIResources(reg, &mockServiceBridge{}); err != nil {
		t.Fatalf("register: %v", err)
	}
	// Eight templates (jobs, runs, runs/timeline, workflows, packs,
	// topics, agents, audit) — verify by listing.
	templates := reg.ListTemplates()
	if len(templates) < 8 {
		t.Errorf("registered %d templates, want >= 8", len(templates))
	}
}
