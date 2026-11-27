package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestPostRetriesOnlyWithNonBlankIdempotencyKey(t *testing.T) {
	cases := []struct {
		name    string
		method  string
		headers map[string]string
		want    int32
	}{
		{"post blank key", http.MethodPost, map[string]string{"Idempotency-Key": ""}, 1},
		{"post whitespace key", http.MethodPost, map[string]string{"Idempotency-Key": "   "}, 1},
		{"post real key", http.MethodPost, map[string]string{"Idempotency-Key": "k-1"}, defaultRequestMaxAttempts},
		{"post real and blank case variants", http.MethodPost, map[string]string{"Idempotency-Key": "k-1", "idempotency-key": "  "}, 1},
		{"get control", http.MethodGet, nil, defaultRequestMaxAttempts},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(http.StatusServiceUnavailable)
			}))
			defer srv.Close()

			c := &Client{BaseURL: srv.URL, HTTPClient: srv.Client()}
			if _, err := c.RequestDetailed(context.Background(), tc.method, "/api/v1/jobs", nil, tc.headers); err == nil {
				t.Fatal("expected an error for a 503 response")
			}
			if got := requests.Load(); got != tc.want {
				t.Fatalf("server received %d requests, want %d", got, tc.want)
			}
		})
	}
}
