package otel

import (
	"context"

	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel"
)

// natsHeaderCarrier implements propagation.TextMapCarrier over nats.Header.
type natsHeaderCarrier struct {
	headers nats.Header
}

func (c natsHeaderCarrier) Get(key string) string {
	return c.headers.Get(key)
}

func (c natsHeaderCarrier) Set(key, value string) {
	c.headers.Set(key, value)
}

func (c natsHeaderCarrier) Keys() []string {
	keys := make([]string, 0, len(c.headers))
	for k := range c.headers {
		keys = append(keys, k)
	}
	return keys
}

// InjectTraceContext writes W3C trace context (traceparent, tracestate) from
// the span in ctx into the provided NATS message headers. If headers is nil,
// it is initialized.
func InjectTraceContext(ctx context.Context, headers *nats.Header) {
	if headers == nil {
		return
	}
	if *headers == nil {
		*headers = nats.Header{}
	}
	otel.GetTextMapPropagator().Inject(ctx, natsHeaderCarrier{headers: *headers})
}

// ExtractTraceContext reads W3C trace context from NATS message headers and
// returns a context enriched with the extracted span context. If headers are
// nil or contain no trace context, the original context is returned unchanged.
func ExtractTraceContext(ctx context.Context, headers nats.Header) context.Context {
	if len(headers) == 0 {
		return ctx
	}
	return otel.GetTextMapPropagator().Extract(ctx, natsHeaderCarrier{headers: headers})
}
