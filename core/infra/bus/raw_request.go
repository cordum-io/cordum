package bus

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	cordumotel "github.com/cordum/cordum/core/infra/otel"
	"github.com/cordum/cordum/core/model"
	"github.com/nats-io/nats.go"
)

const (
	rawRequestMaxPayloadBytes = 1 << 20
	rawRequestHandlerTimeout  = 5 * time.Second
)

var (
	errEmptyQueue         = errors.New("empty queue")
	errRawPayloadTooLarge = errors.New("raw request payload exceeds limit")
	errRawHandlerTimeout  = errors.New("raw request handler timed out")
	errRawHandlerPanic    = errors.New("raw request handler panicked")
)

type rawRequestConfig struct {
	maxPayloadBytes int
	handlerTimeout  time.Duration
}

var defaultRawRequestConfig = rawRequestConfig{
	maxPayloadBytes: rawRequestMaxPayloadBytes,
	handlerTimeout:  rawRequestHandlerTimeout,
}

// QueueRespond registers a core-NATS queue subscriber for bounded raw
// request/reply traffic. It deliberately bypasses JetStream persistence.
func (b *NatsBus) QueueRespond(subject, queue string, handler model.RawRequestHandler) (model.BusSubscription, error) {
	if b == nil || b.nc == nil {
		return nil, errNilBus
	}
	if subject == "" {
		return nil, errEmptyTopic
	}
	if strings.TrimSpace(queue) == "" {
		return nil, errEmptyQueue
	}
	if handler == nil {
		return nil, errors.New("nil handler")
	}

	sub, err := b.nc.QueueSubscribe(subject, queue, func(msg *nats.Msg) {
		b.processRawRequest(msg, handler, defaultRawRequestConfig)
	})
	if err != nil {
		return nil, fmt.Errorf("queue respond %s: %w", subject, err)
	}
	if err := b.nc.Flush(); err != nil {
		_ = sub.Drain()
		return nil, fmt.Errorf("queue respond %s flush: %w", subject, err)
	}
	if !b.trackSub(sub) {
		_ = sub.Drain()
		return nil, errors.New("bus is draining")
	}
	return sub, nil
}

func (b *NatsBus) processRawRequest(msg *nats.Msg, handler model.RawRequestHandler, config rawRequestConfig) {
	if msg == nil || msg.Reply == "" {
		return
	}
	ctx := cordumotel.ExtractTraceContext(context.Background(), msg.Header)
	response, err := executeRawHandler(ctx, msg.Data, config, handler)
	if err != nil {
		logRawRequestFailure(msg.Subject, err)
		return
	}
	if b == nil || b.nc == nil {
		return
	}

	reply := &nats.Msg{Subject: msg.Reply, Data: []byte(response), Header: nats.Header{}}
	cordumotel.InjectTraceContext(ctx, &reply.Header)
	if err := b.nc.PublishMsg(reply); err != nil {
		slog.Warn("bus: raw request reply failed", "subject", msg.Subject, "err", err)
	}
}

func executeRawHandler(parent context.Context, request []byte, config rawRequestConfig, handler model.RawRequestHandler) (model.RawResponse, error) {
	if len(request) > config.maxPayloadBytes {
		return nil, errRawPayloadTooLarge
	}
	ctx, cancel := context.WithTimeout(parent, config.handlerTimeout)
	defer cancel()

	requestCopy := append(model.RawRequest(nil), request...)
	response, err := callRawHandler(ctx, requestCopy, handler)
	if ctx.Err() != nil {
		return nil, errRawHandlerTimeout
	}
	if err != nil {
		return nil, err
	}
	if len(response) > config.maxPayloadBytes {
		return nil, errRawPayloadTooLarge
	}
	return response, nil
}

func callRawHandler(ctx context.Context, request model.RawRequest, handler model.RawRequestHandler) (response model.RawResponse, err error) {
	defer func() {
		if recover() != nil {
			response, err = nil, errRawHandlerPanic
		}
	}()
	return handler(ctx, request)
}

func logRawRequestFailure(subject string, err error) {
	reason := "handler_error"
	switch {
	case errors.Is(err, errRawPayloadTooLarge):
		reason = "payload_too_large"
	case errors.Is(err, errRawHandlerTimeout):
		reason = "handler_timeout"
	case errors.Is(err, errRawHandlerPanic):
		reason = "handler_panic"
	}
	slog.Warn("bus: raw request rejected", "subject", subject, "reason", reason)
}
