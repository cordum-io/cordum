package perfharness

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestQueueSamples(t *testing.T) {
	for _, tc := range []struct {
		name, kind, body string
		status           int
		want             *float64
	}{
		{"prometheus", "prometheus", "# TYPE queue gauge\nqueue 7\n", 200, number(7)},
		{"jsz", "nats_jsz", `{"account_details":[{"stream_detail":[{"name":"jobs","state":{"messages":12}}]}]}`, 200, number(12)},
		{"zero", "prometheus", "queue 0\n", 200, number(0)},
		{"http", "prometheus", "queue 1\n", 500, nil},
		{"parse", "prometheus", "queue invalid\n", 200, nil},
		{"missing metric", "prometheus", "other 8\n", 200, nil},
		{"missing stream", "nats_jsz", `{"account_details":[]}`, 200, nil},
		{"missing count", "nats_jsz", `{"account_details":[{"stream_detail":[{"name":"jobs","state":{}}]}]}`, 200, nil},
		{"json", "nats_jsz", "{", 200, nil},
		{"nan", "prometheus", "queue NaN\n", 200, nil},
		{"negative", "prometheus", "queue -1\n", 200, nil},
		{"ambiguous", "prometheus", "queue{tenant=\"a\"} 1\nqueue{tenant=\"b\"} 2\n", 200, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.kind == "nats_jsz" && (r.URL.Query().Get("streams") != "true" || r.URL.Query().Get("accounts") != "true") {
					t.Error("missing jsz detail query")
				}
				if r.Header.Get("X-API-Key") != "" {
					t.Error("queue received API key")
				}
				w.WriteHeader(tc.status)
				writeFixture(t, w, tc.body)
			}))
			defer server.Close()
			q := QueueSource{Kind: tc.kind, Metric: "queue", Stream: "jobs", IntervalMS: 10}
			got := sampleQueue(context.Background(), server.Client(), server.URL, q)
			if (got == nil) != (tc.want == nil) || got != nil && *got != *tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func number(n float64) *float64 { return &n }

func TestQueueTimeSeries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Millisecond)
	defer cancel()
	start := time.Now()
	samples := collectQueue(ctx, server.Client(), server.URL, QueueSource{Kind: "prometheus", Metric: "queue", IntervalMS: 10}, start)
	if len(samples) < 2 {
		t.Fatalf("samples %d", len(samples))
	}
	for i, s := range samples {
		if s.Value != nil {
			t.Fatal("gap converted to zero")
		}
		if s.TMS < 0 || i > 0 && s.TMS <= samples[i-1].TMS {
			t.Fatal("unordered samples")
		}
	}
}
