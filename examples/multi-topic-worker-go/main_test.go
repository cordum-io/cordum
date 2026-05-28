package main

import (
	"encoding/json"
	"testing"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	"github.com/cordum/cordum/sdk/runtime"
)

// makeCtx builds a runtime.Context whose Job.Topic is `topic` — the minimum
// surface our dispatcher cares about. Other fields stay zero / nil.
func makeCtx(topic string) runtime.Context {
	return runtime.Context{
		Job: &agentv1.JobRequest{Topic: topic},
	}
}

func TestDispatcher_RoutesToCorrectTypedHandler(t *testing.T) {
	d := makeDispatcher()

	t.Run("upper", func(t *testing.T) {
		raw, _ := json.Marshal(upperIn{Text: "hello"})
		got, err := d(makeCtx(topicUpper), raw)
		if err != nil {
			t.Fatalf("upper: %v", err)
		}
		var out upperOut
		if err := json.Unmarshal(got, &out); err != nil {
			t.Fatalf("decode upperOut: %v", err)
		}
		if out.Text != "HELLO" {
			t.Errorf("upper: got %q, want %q", out.Text, "HELLO")
		}
	})

	t.Run("add", func(t *testing.T) {
		raw, _ := json.Marshal(addIn{A: 2, B: 40})
		got, err := d(makeCtx(topicAdd), raw)
		if err != nil {
			t.Fatalf("add: %v", err)
		}
		var out addOut
		if err := json.Unmarshal(got, &out); err != nil {
			t.Fatalf("decode addOut: %v", err)
		}
		if out.Sum != 42 {
			t.Errorf("add: got %d, want 42", out.Sum)
		}
	})

	t.Run("tag", func(t *testing.T) {
		raw, _ := json.Marshal(tagIn{Items: []string{"a", "b"}, Tag: "x"})
		got, err := d(makeCtx(topicTag), raw)
		if err != nil {
			t.Fatalf("tag: %v", err)
		}
		var out tagOut
		if err := json.Unmarshal(got, &out); err != nil {
			t.Fatalf("decode tagOut: %v", err)
		}
		want := []string{"x:a", "x:b"}
		if len(out.Tagged) != 2 || out.Tagged[0] != want[0] || out.Tagged[1] != want[1] {
			t.Errorf("tag: got %v, want %v", out.Tagged, want)
		}
	})
}

// The regression this whole example exists to prevent: even when the
// scheduler delivers via the direct subject (not the topic subject), the
// dispatcher must still pick the right handler based on ctx.Job.Topic.
// A naïve handler bound to the direct subject would silently mis-route here.
func TestDispatcher_DirectSubjectStillUsesJobTopic(t *testing.T) {
	d := makeDispatcher()

	// Simulate delivery via the direct subject for each of the three topics
	// and confirm each still routes to its correct typed handler. The CAP
	// runtime always populates ctx.Job.Topic from the JobRequest payload,
	// independent of the NATS subject the frame arrived on.
	cases := []struct {
		name   string
		topic  string
		in     any
		assert func(t *testing.T, raw json.RawMessage)
	}{
		{
			name:  "upper-via-direct",
			topic: topicUpper,
			in:    upperIn{Text: "go"},
			assert: func(t *testing.T, raw json.RawMessage) {
				var out upperOut
				_ = json.Unmarshal(raw, &out)
				if out.Text != "GO" {
					t.Errorf("got %q, want GO", out.Text)
				}
			},
		},
		{
			name:  "add-via-direct",
			topic: topicAdd,
			in:    addIn{A: 1, B: 1},
			assert: func(t *testing.T, raw json.RawMessage) {
				var out addOut
				_ = json.Unmarshal(raw, &out)
				if out.Sum != 2 {
					t.Errorf("got %d, want 2", out.Sum)
				}
			},
		},
		{
			// Third typed handler — covers the gap CodeRabbit flagged on #315:
			// the regression case had `upper` and `add` but not `tag`, leaving
			// one of the three typed handlers unverified for the direct-subject
			// path that this whole example exists to teach.
			name:  "tag-via-direct",
			topic: topicTag,
			in:    tagIn{Items: []string{"a"}, Tag: "p"},
			assert: func(t *testing.T, raw json.RawMessage) {
				var out tagOut
				_ = json.Unmarshal(raw, &out)
				if len(out.Tagged) != 1 || out.Tagged[0] != "p:a" {
					t.Errorf("got %v, want [p:a]", out.Tagged)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload, _ := json.Marshal(tc.in)
			// Same dispatcher, same ctx topic — only the subject delivery
			// path differs in production, which the dispatcher does NOT see.
			out, err := d(makeCtx(tc.topic), payload)
			if err != nil {
				t.Fatalf("dispatcher err: %v", err)
			}
			tc.assert(t, out)
		})
	}
}

func TestDispatcher_UnknownTopicReturnsTypedError(t *testing.T) {
	d := makeDispatcher()
	_, err := d(makeCtx("job.multi-topic.does-not-exist"), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for unknown topic, got nil")
	}
	// Error message should include the offending topic name so an operator
	// staring at scheduler logs can identify the misroute immediately.
	if !contains(err.Error(), "job.multi-topic.does-not-exist") {
		t.Errorf("error missing topic name: %q", err)
	}
}

func TestDispatcher_NilJobReturnsEmptyTopicError(t *testing.T) {
	d := makeDispatcher()
	// Defensive: ctx.Job nil should not panic; should surface as unknown-topic.
	_, err := d(runtime.Context{}, json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for nil Job, got nil")
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
