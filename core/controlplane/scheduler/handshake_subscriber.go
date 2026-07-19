package scheduler

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	"github.com/cordum/cordum/core/model"
	"google.golang.org/protobuf/proto"
)

const maximumHandshakePacketBytes = 64 * 1024

var (
	errHandshakeSubscriberStarted = errors.New("scheduler: worker handshake subscriber already started")
	errHandshakeRequestRejected   = errors.New("scheduler: worker handshake rejected")
)

type handshakeRawResponder interface {
	QueueRespond(string, string, model.RawRequestHandler) (model.BusSubscription, error)
}

type handshakeProtocolService interface {
	HandleChallenge(context.Context, *agentv1.BusPacket) (*agentv1.BusPacket, error)
	HandleAuthenticate(context.Context, *agentv1.BusPacket) (*agentv1.BusPacket, error)
}

// HandshakeSubscriber exposes the authenticated trust protocol over two HA
// core-NATS request/reply endpoints. Legacy handshake subjects are excluded.
type HandshakeSubscriber struct {
	mu            sync.Mutex
	bus           handshakeRawResponder
	service       handshakeProtocolService
	subscriptions []model.BusSubscription
	started       bool
}

// NewHandshakeSubscriber creates an inert responder. Start must succeed before
// the scheduler accepts work so partial security wiring fails boot closed.
func NewHandshakeSubscriber(bus handshakeRawResponder, service handshakeProtocolService) (*HandshakeSubscriber, error) {
	if missingHandshakeDependency(reflect.ValueOf(bus)) || missingHandshakeDependency(reflect.ValueOf(service)) {
		return nil, errors.New("scheduler: worker handshake subscriber dependencies required")
	}
	return &HandshakeSubscriber{bus: bus, service: service}, nil
}

// Start installs exactly one queue responder for each authenticated phase.
func (s *HandshakeSubscriber) Start() error {
	if s == nil {
		return errors.New("scheduler: worker handshake subscriber required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if missingHandshakeDependency(reflect.ValueOf(s.bus)) || missingHandshakeDependency(reflect.ValueOf(s.service)) {
		return errors.New("scheduler: worker handshake subscriber dependencies required")
	}
	if s.started {
		return errHandshakeSubscriberStarted
	}
	subscriptions, err := s.registerResponders()
	if err != nil {
		return err
	}
	s.subscriptions = subscriptions
	s.started = true
	return nil
}

func (s *HandshakeSubscriber) registerResponders() ([]model.BusSubscription, error) {
	registrations := []struct {
		subject string
		handler model.RawRequestHandler
	}{
		{WorkerHandshakeChallengeSubject, s.handleChallenge},
		{WorkerHandshakeAuthenticateSubject, s.handleAuthenticate},
	}
	subscriptions := make([]model.BusSubscription, 0, len(registrations))
	for _, registration := range registrations {
		subscription, err := s.bus.QueueRespond(registration.subject, schedulerQueue, registration.handler)
		if err == nil && missingHandshakeDependency(reflect.ValueOf(subscription)) {
			err = errors.New("subscription unavailable")
		}
		if err != nil {
			cleanupErr := unsubscribeHandshakeSubscriptions(subscriptions)
			return nil, errors.Join(fmt.Errorf("scheduler: subscribe %s: %w", registration.subject, err), cleanupErr)
		}
		subscriptions = append(subscriptions, subscription)
	}
	return subscriptions, nil
}

func (s *HandshakeSubscriber) handleChallenge(ctx context.Context, request model.RawRequest) (model.RawResponse, error) {
	packet, err := decodeHandshakeRequest(request)
	if err != nil {
		return nil, err
	}
	response, serviceErr := s.service.HandleChallenge(ctx, packet)
	if serviceErr != nil || response == nil || response.GetWorkerHandshakeChallenge() == nil {
		return nil, errHandshakeRequestRejected
	}
	return encodeHandshakeResponse(response)
}

func (s *HandshakeSubscriber) handleAuthenticate(ctx context.Context, request model.RawRequest) (model.RawResponse, error) {
	packet, err := decodeHandshakeRequest(request)
	if err != nil {
		return nil, err
	}
	response, serviceErr := s.service.HandleAuthenticate(ctx, packet)
	if serviceErr != nil || response == nil || response.GetWorkerHandshakeResult() == nil {
		return nil, errHandshakeRequestRejected
	}
	return encodeHandshakeResponse(response)
}

func decodeHandshakeRequest(request model.RawRequest) (*agentv1.BusPacket, error) {
	if len(request) == 0 || len(request) > maximumHandshakePacketBytes {
		return nil, errHandshakeRequestRejected
	}
	packet := &agentv1.BusPacket{}
	if err := (proto.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(request, packet); err != nil {
		return nil, errHandshakeRequestRejected
	}
	return packet, nil
}

func encodeHandshakeResponse(packet *agentv1.BusPacket) (model.RawResponse, error) {
	data, err := (proto.MarshalOptions{Deterministic: true}).Marshal(packet)
	if err != nil || len(data) == 0 || len(data) > maximumHandshakePacketBytes {
		return nil, errHandshakeRequestRejected
	}
	return model.RawResponse(data), nil
}

// Close removes both responders and is safe to call more than once.
func (s *HandshakeSubscriber) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	subscriptions := s.subscriptions
	s.subscriptions = nil
	s.started = false
	return unsubscribeHandshakeSubscriptions(subscriptions)
}

func unsubscribeHandshakeSubscriptions(subscriptions []model.BusSubscription) error {
	var failures []error
	for _, subscription := range subscriptions {
		if missingHandshakeDependency(reflect.ValueOf(subscription)) {
			continue
		}
		if err := drainHandshakeSubscription(subscription); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func drainHandshakeSubscription(subscription model.BusSubscription) error {
	drainer, ok := subscription.(interface{ Drain() error })
	if !ok {
		return subscription.Unsubscribe()
	}
	if err := drainer.Drain(); err != nil {
		return errors.Join(err, subscription.Unsubscribe())
	}
	return nil
}
