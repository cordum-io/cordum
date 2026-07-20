package bus

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cordum/cordum/core/model"
	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

var _ model.RawRequestResponder = (*NatsBus)(nil)
var _ model.BusSubscription = (*nats.Subscription)(nil)

func TestQueueRespondUsesCoreNATSAndPreservesTrace(t *testing.T) {
	previous := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTextMapPropagator(previous) })

	ns := startTestNATSServer(t, true)
	bus := newTestNatsBus(t, ns, true)
	const subject = "job.raw-request"
	traceparent := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

	var handlerTrace trace.SpanContext
	sub, err := bus.QueueRespond(subject, "raw-responders", func(ctx context.Context, request model.RawRequest) (model.RawResponse, error) {
		handlerTrace = trace.SpanContextFromContext(ctx)
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > rawRequestHandlerTimeout {
			t.Errorf("handler deadline not safely bounded: %v, %v", deadline, ok)
		}
		return model.RawResponse("reply:" + string(request)), nil
	})
	if err != nil {
		t.Fatalf("QueueRespond: %v", err)
	}
	if sub == nil {
		t.Fatal("QueueRespond returned nil subscription")
	}
	if err := bus.nc.Flush(); err != nil {
		t.Fatalf("flush subscription: %v", err)
	}

	request := nats.NewMsg(subject)
	request.Data = []byte("hello")
	request.Header.Set("traceparent", traceparent)
	response, err := bus.nc.RequestMsg(request, time.Second)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if got := string(response.Data); got != "reply:hello" {
		t.Fatalf("response = %q, want %q", got, "reply:hello")
	}
	if !handlerTrace.IsRemote() || handlerTrace.TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("handler trace context = %v", handlerTrace)
	}
	if got := response.Header.Get("traceparent"); got != traceparent {
		t.Fatalf("response traceparent = %q, want %q", got, traceparent)
	}
	assertTrackedSubscriptionCount(t, bus, 1)
}

func TestQueueRespondValidatesArguments(t *testing.T) {
	handler := func(context.Context, model.RawRequest) (model.RawResponse, error) {
		return nil, nil
	}
	tests := []struct {
		name    string
		bus     *NatsBus
		subject string
		queue   string
		handler model.RawRequestHandler
	}{
		{name: "nil bus", bus: nil, subject: "rpc.test", queue: "workers", handler: handler},
		{name: "nil connection", bus: &NatsBus{}, subject: "rpc.test", queue: "workers", handler: handler},
		{name: "empty subject", bus: &NatsBus{nc: &nats.Conn{}}, queue: "workers", handler: handler},
		{name: "empty queue", bus: &NatsBus{nc: &nats.Conn{}}, subject: "rpc.test", handler: handler},
		{name: "nil handler", bus: &NatsBus{nc: &nats.Conn{}}, subject: "rpc.test", queue: "workers"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.bus.QueueRespond(test.subject, test.queue, test.handler); err == nil {
				t.Fatal("QueueRespond error = nil")
			}
		})
	}
}

func TestRawRequestRequiresReplySubject(t *testing.T) {
	var calls atomic.Int32
	handler := func(context.Context, model.RawRequest) (model.RawResponse, error) {
		calls.Add(1)
		return model.RawResponse("response"), nil
	}
	bus := &NatsBus{}
	bus.processRawRequest(&nats.Msg{Subject: "rpc.test", Data: []byte("request")}, handler, defaultRawRequestConfig)
	if got := calls.Load(); got != 0 {
		t.Fatalf("handler calls = %d, want 0", got)
	}
}

func TestExecuteRawHandlerRejectsOversizeRequest(t *testing.T) {
	var calls atomic.Int32
	config := rawRequestConfig{maxPayloadBytes: 4, handlerTimeout: time.Second}
	_, err := executeRawHandler(context.Background(), []byte("12345"), config, func(context.Context, model.RawRequest) (model.RawResponse, error) {
		calls.Add(1)
		return nil, nil
	})
	if !errors.Is(err, errRawPayloadTooLarge) {
		t.Fatalf("error = %v, want %v", err, errRawPayloadTooLarge)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("handler calls = %d, want 0", got)
	}
}

func TestExecuteRawHandlerRejectsOversizeResponse(t *testing.T) {
	config := rawRequestConfig{maxPayloadBytes: 4, handlerTimeout: time.Second}
	_, err := executeRawHandler(context.Background(), []byte("1234"), config, func(context.Context, model.RawRequest) (model.RawResponse, error) {
		return model.RawResponse("12345"), nil
	})
	if !errors.Is(err, errRawPayloadTooLarge) {
		t.Fatalf("error = %v, want %v", err, errRawPayloadTooLarge)
	}
}

func TestExecuteRawHandlerEnforcesDeadline(t *testing.T) {
	release := make(chan struct{})
	config := rawRequestConfig{maxPayloadBytes: 32, handlerTimeout: 20 * time.Millisecond}
	started := time.Now()
	_, err := executeRawHandler(context.Background(), []byte("request"), config, func(ctx context.Context, _ model.RawRequest) (model.RawResponse, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("handler context has no deadline")
		}
		<-release
		return model.RawResponse("late"), nil
	})
	close(release)
	if !errors.Is(err, errRawHandlerTimeout) {
		t.Fatalf("error = %v, want %v", err, errRawHandlerTimeout)
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("handler timeout took %v", elapsed)
	}
}

func TestExecuteRawHandlerRecoversPanic(t *testing.T) {
	config := rawRequestConfig{maxPayloadBytes: 32, handlerTimeout: time.Second}
	_, err := executeRawHandler(context.Background(), []byte("request"), config, func(context.Context, model.RawRequest) (model.RawResponse, error) {
		panic("secret panic text")
	})
	if !errors.Is(err, errRawHandlerPanic) {
		t.Fatalf("error = %v, want %v", err, errRawHandlerPanic)
	}
}

func TestProcessRawRequestDoesNotPublishRejectedPayload(t *testing.T) {
	ns := startTestNATSServer(t, false)
	bus := newTestNatsBus(t, ns, false)
	config := rawRequestConfig{maxPayloadBytes: 4, handlerTimeout: time.Second}
	tests := []struct {
		name       string
		request    []byte
		response   model.RawResponse
		handlerErr error
		wantCalls  int32
	}{
		{name: "oversize request", request: []byte("12345"), response: model.RawResponse("ok")},
		{name: "oversize response", request: []byte("1234"), response: model.RawResponse("12345"), wantCalls: 1},
		{name: "handler error", request: []byte("1234"), handlerErr: errors.New("handler failed"), wantCalls: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reply := nats.NewInbox()
			replies, err := bus.nc.SubscribeSync(reply)
			if err != nil {
				t.Fatalf("subscribe reply: %v", err)
			}
			defer func() {
				if err := replies.Unsubscribe(); err != nil {
					t.Errorf("unsubscribe reply: %v", err)
				}
			}()
			if err := bus.nc.Flush(); err != nil {
				t.Fatalf("flush reply subscription: %v", err)
			}

			var calls atomic.Int32
			bus.processRawRequest(&nats.Msg{Subject: "rpc.test", Reply: reply, Data: test.request}, func(context.Context, model.RawRequest) (model.RawResponse, error) {
				calls.Add(1)
				return test.response, test.handlerErr
			}, config)
			if _, err := replies.NextMsg(25 * time.Millisecond); !errors.Is(err, nats.ErrTimeout) {
				t.Fatalf("reply error = %v, want %v", err, nats.ErrTimeout)
			}
			if got := calls.Load(); got != test.wantCalls {
				t.Fatalf("handler calls = %d, want %d", got, test.wantCalls)
			}
		})
	}
}

func TestQueueRespondSubscriptionIsDrained(t *testing.T) {
	ns := startTestNATSServer(t, false)
	bus := newTestNatsBus(t, ns, false)
	subscription, err := bus.QueueRespond("rpc.drain", "raw-responders", func(context.Context, model.RawRequest) (model.RawResponse, error) {
		return model.RawResponse("ok"), nil
	})
	if err != nil {
		t.Fatalf("QueueRespond: %v", err)
	}
	assertTrackedSubscriptionCount(t, bus, 1)
	bus.Drain()
	assertTrackedSubscriptionCount(t, bus, 0)

	natsSub, ok := subscription.(*nats.Subscription)
	if !ok {
		t.Fatalf("subscription type = %T", subscription)
	}
	deadline := time.Now().Add(time.Second)
	for natsSub.IsValid() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if natsSub.IsValid() {
		t.Fatal("subscription remains valid after Drain")
	}
}

func assertTrackedSubscriptionCount(t *testing.T, bus *NatsBus, want int) {
	t.Helper()
	bus.subsMu.Lock()
	defer bus.subsMu.Unlock()
	if got := len(bus.subs); got != want {
		t.Fatalf("tracked subscriptions = %d, want %d", got, want)
	}
}
