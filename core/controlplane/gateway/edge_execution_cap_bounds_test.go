package gateway

import (
	"strconv"
	"testing"

	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/prometheus/client_golang/prometheus"
)

func TestEdgeMaxExecutionsPerSessionNativeIntBounds(t *testing.T) {
	maxInt := int64(^uint(0) >> 1)
	cases := []struct {
		name string
		raw  string
		want int64
	}{
		{name: "missing", want: edgecore.DefaultMaxExecutionsPerSession},
		{name: "blank", raw: " \t ", want: edgecore.DefaultMaxExecutionsPerSession},
		{name: "invalid", raw: "not-a-number", want: edgecore.DefaultMaxExecutionsPerSession},
		{name: "zero", raw: "0", want: edgecore.DefaultMaxExecutionsPerSession},
		{name: "negative", raw: "-1", want: edgecore.DefaultMaxExecutionsPerSession},
		{name: "minimum", raw: "1", want: 1},
		{name: "trimmed", raw: " 123 ", want: 123},
		{name: "maximum", raw: strconv.FormatInt(maxInt, 10), want: maxInt},
		{name: "above-maximum", raw: strconv.FormatUint(uint64(maxInt)+1, 10), want: edgecore.DefaultMaxExecutionsPerSession},
		{name: "beyond-int64", raw: "9223372036854775808", want: edgecore.DefaultMaxExecutionsPerSession},
	}
	if strconv.IntSize == 32 {
		cases = append(cases, struct {
			name string
			raw  string
			want int64
		}{name: "would-wrap-to-one", raw: "4294967297", want: edgecore.DefaultMaxExecutionsPerSession})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CORDUM_EDGE_MAX_EXECUTIONS_PER_SESSION", tc.raw)
			if got := int64(edgeMaxExecutionsPerSession()); got != tc.want {
				t.Errorf("resolved execution cap = %d, want %d", got, tc.want)
			}
			// The HTTP pre-check, store enforcement and MCP session rotation
			// must all use the same cap, including invalid environment values.
			store, _ := newGatewayEdgeStore(nil, prometheus.NewRegistry(), 0)
			storeCap, _ := store.ExecutionCaps()
			if int64(storeCap) != tc.want {
				t.Errorf("store execution cap = %d, want %d", storeCap, tc.want)
			}
		})
	}
}
