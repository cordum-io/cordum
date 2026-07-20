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

type rawHandlerResult struct {
	response model.RawResponse
	err      error
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
	b.trackSub(sub)
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

	resultCh := make(chan rawHandlerResult, 1)
	requestCopy := append(model.RawRequest(nil), request...)
	go callRawHandler(ctx, requestCopy, handler, resultCh)
	select {
	case result := <-resultCh:
		if ctx.Err() != nil {
			return nil, errRawHandlerTimeout
		}
		if result.err != nil {
			return nil, result.err
		}
		if len(result.response) > config.maxPayloadBytes {
			return nil, errRawPayloadTooLarge
		}
		return result.response, nil
	case <-ctx.Done():
		return nil, errRawHandlerTimeout
	}
}

func callRawHandler(ctx context.Context, request model.RawRequest, handler model.RawRequestHandler, resultCh chan<- rawHandlerResult) {
	defer func() {
		if recover() != nil {
			resultCh <- rawHandlerResult{err: errRawHandlerPanic}
		}
	}()
	response, err := handler(ctx, request)
	resultCh <- rawHandlerResult{response: response, err: err}
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
