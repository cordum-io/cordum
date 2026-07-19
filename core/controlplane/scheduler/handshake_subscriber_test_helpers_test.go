package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	"github.com/cordum/cordum/core/model"
	"google.golang.org/protobuf/proto"
)

var errNoHandshakeRegistration = errors.New("no handshake registration")

type fakeHandshakeRegistration struct {
	subject string
	queue   string
	handler model.RawRequestHandler
	sub     *fakeHandshakeSubscription
}

type fakeHandshakeResponder struct {
	mu             sync.Mutex
	registrations  []fakeHandshakeRegistration
	failSubject    string
	subscribeErr   error
	unsubscribeErr error
}

func (f *fakeHandshakeResponder) QueueRespond(subject, queue string, handler model.RawRequestHandler) (model.BusSubscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if subject == f.failSubject {
		return nil, f.subscribeErr
	}
	sub := &fakeHandshakeSubscription{err: f.unsubscribeErr}
	f.registrations = append(f.registrations, fakeHandshakeRegistration{
		subject: subject, queue: queue, handler: handler, sub: sub,
	})
	return sub, nil
}

func (f *fakeHandshakeResponder) snapshot() []fakeHandshakeRegistration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeHandshakeRegistration(nil), f.registrations...)
}

func (f *fakeHandshakeResponder) invoke(ctx context.Context, subject string, request model.RawRequest) (model.RawResponse, error) {
	for _, registration := range f.snapshot() {
		if registration.subject == subject && registration.sub.active() {
			return registration.handler(ctx, request)
		}
	}
	return nil, errNoHandshakeRegistration
}

type fakeHandshakeSubscription struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (s *fakeHandshakeSubscription) Unsubscribe() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.err
}

func (s *fakeHandshakeSubscription) active() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls == 0
}

func (s *fakeHandshakeSubscription) unsubscribeCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

type fakeHandshakeProtocolService struct {
	mu                sync.Mutex
	challengeResponse *agentv1.BusPacket
	authResponse      *agentv1.BusPacket
	challengeErr      error
	authErr           error
	challengePackets  []*agentv1.BusPacket
	authPackets       []*agentv1.BusPacket
	challengeContext  context.Context
	authContext       context.Context
}

func (s *fakeHandshakeProtocolService) HandleChallenge(ctx context.Context, packet *agentv1.BusPacket) (*agentv1.BusPacket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.challengeContext = ctx
	s.challengePackets = append(s.challengePackets, packet)
	return s.challengeResponse, s.challengeErr
}

func (s *fakeHandshakeProtocolService) HandleAuthenticate(ctx context.Context, packet *agentv1.BusPacket) (*agentv1.BusPacket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authContext = ctx
	s.authPackets = append(s.authPackets, packet)
	return s.authResponse, s.authErr
}

func (s *fakeHandshakeProtocolService) counts() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.challengePackets), len(s.authPackets)
}

func (s *fakeHandshakeProtocolService) contexts() (context.Context, context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.challengeContext, s.authContext
}

func marshalHandshakeRaw(t *testing.T, packet *agentv1.BusPacket) model.RawRequest {
	t.Helper()
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(packet)
	if err != nil {
		t.Fatalf("marshal handshake packet: %v", err)
	}
	return model.RawRequest(data)
}

func decodeHandshakeRaw(t *testing.T, response model.RawResponse) *agentv1.BusPacket {
	t.Helper()
	packet := &agentv1.BusPacket{}
	if err := proto.Unmarshal(response, packet); err != nil {
		t.Fatalf("unmarshal handshake response: %v", err)
	}
	return packet
}
