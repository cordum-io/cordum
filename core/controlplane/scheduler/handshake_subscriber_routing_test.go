package scheduler

import (
	"context"
	"errors"
	"strings"
	"testing"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	"github.com/cordum/cordum/core/model"
	"google.golang.org/protobuf/proto"
)

func TestHandshakeSubscriberRoutesTypedPacketsAndPreservesUnknownFields(t *testing.T) {
	challengeResponse, authResponse := handshakeRouteResponses()
	service := &fakeHandshakeProtocolService{
		challengeResponse: challengeResponse, authResponse: authResponse,
	}
	bus := &fakeHandshakeResponder{}
	subscriber, err := NewHandshakeSubscriber(bus, service)
	if err != nil {
		t.Fatalf("new subscriber: %v", err)
	}
	if err := subscriber.Start(); err != nil {
		t.Fatalf("start subscriber: %v", err)
	}
	defer func() {
		if err := subscriber.Close(); err != nil {
			t.Errorf("close subscriber: %v", err)
		}
	}()
	challengeContext := invokeChallengeRoute(t, bus, challengeResponse)
	authContext := invokeAuthenticateRoute(t, bus, authResponse)
	assertHandshakeServiceRoutes(t, service, challengeContext, authContext)
}

func handshakeRouteResponses() (*agentv1.BusPacket, *agentv1.BusPacket) {
	challenge := &agentv1.BusPacket{
		TraceId: "challenge-response",
		Payload: &agentv1.BusPacket_WorkerHandshakeChallenge{
			WorkerHandshakeChallenge: &agentv1.WorkerHandshakeChallenge{ChallengeId: "challenge-1"},
		},
	}
	authenticate := &agentv1.BusPacket{
		TraceId: "authenticate-response",
		Payload: &agentv1.BusPacket_WorkerHandshakeResult{
			WorkerHandshakeResult: &agentv1.WorkerHandshakeResult{Accepted: true},
		},
	}
	return challenge, authenticate
}

func invokeChallengeRoute(t *testing.T, bus *fakeHandshakeResponder, want *agentv1.BusPacket) context.Context {
	t.Helper()
	request := &agentv1.BusPacket{
		TraceId: "challenge-request",
		Payload: &agentv1.BusPacket_WorkerHandshakeChallengeRequest{
			WorkerHandshakeChallengeRequest: &agentv1.WorkerHandshakeChallengeRequest{RequestId: "request-1"},
		},
	}
	rawRequest := append(marshalHandshakeRaw(t, request), 0xa0, 0x06, 0x01)
	ctx := context.WithValue(context.Background(), handshakeContextKey{}, "challenge")
	response, err := bus.invoke(ctx, WorkerHandshakeChallengeSubject, rawRequest)
	if err != nil {
		t.Fatalf("challenge response: %v", err)
	}
	if got := decodeHandshakeRaw(t, response); !proto.Equal(got, want) {
		t.Fatalf("challenge response = %v, want %v", got, want)
	}
	return ctx
}

func invokeAuthenticateRoute(t *testing.T, bus *fakeHandshakeResponder, want *agentv1.BusPacket) context.Context {
	t.Helper()
	request := &agentv1.BusPacket{
		TraceId: "authenticate-request",
		Payload: &agentv1.BusPacket_WorkerHandshakeAuthenticate{
			WorkerHandshakeAuthenticate: &agentv1.WorkerHandshakeAuthenticate{},
		},
	}
	ctx := context.WithValue(context.Background(), handshakeContextKey{}, "authenticate")
	response, err := bus.invoke(ctx, WorkerHandshakeAuthenticateSubject, marshalHandshakeRaw(t, request))
	if err != nil {
		t.Fatalf("authenticate response: %v", err)
	}
	if got := decodeHandshakeRaw(t, response); !proto.Equal(got, want) {
		t.Fatalf("authenticate response = %v, want %v", got, want)
	}
	return ctx
}

func assertHandshakeServiceRoutes(t *testing.T, service *fakeHandshakeProtocolService, challengeCtx, authCtx context.Context) {
	t.Helper()
	challengeCalls, authCalls := service.counts()
	if challengeCalls != 1 || authCalls != 1 {
		t.Fatalf("service calls challenge=%d authenticate=%d, want 1 each", challengeCalls, authCalls)
	}
	gotChallengeContext, gotAuthContext := service.contexts()
	if gotChallengeContext != challengeCtx || gotAuthContext != authCtx {
		t.Fatal("transport request context was not preserved")
	}
	if unknown := service.challengePackets[0].ProtoReflect().GetUnknown(); len(unknown) == 0 {
		t.Fatal("unknown protobuf fields were discarded before security validation")
	}
}

type handshakeContextKey struct{}

type opaqueHandshakeCase struct {
	name       string
	request    model.RawRequest
	response   *agentv1.BusPacket
	serviceErr error
	wantCalls  int
}

func TestHandshakeSubscriberRejectsBadInputAndServiceFailuresOpaquely(t *testing.T) {
	for _, test := range opaqueHandshakeCases(t) {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeHandshakeProtocolService{challengeResponse: test.response, challengeErr: test.serviceErr}
			bus := &fakeHandshakeResponder{}
			subscriber, err := NewHandshakeSubscriber(bus, service)
			if err != nil {
				t.Fatalf("new subscriber: %v", err)
			}
			if err := subscriber.Start(); err != nil {
				t.Fatalf("start subscriber: %v", err)
			}
			defer func() {
				if err := subscriber.Close(); err != nil {
					t.Errorf("close subscriber: %v", err)
				}
			}()
			assertOpaqueChallengeRejection(t, bus, service, test)
		})
	}
}

func opaqueHandshakeCases(t *testing.T) []opaqueHandshakeCase {
	t.Helper()
	request := marshalHandshakeRaw(t, &agentv1.BusPacket{TraceId: "request"})
	wrongPhase := &agentv1.BusPacket{
		Payload: &agentv1.BusPacket_WorkerHandshakeResult{
			WorkerHandshakeResult: &agentv1.WorkerHandshakeResult{Accepted: true},
		},
	}
	oversized := &agentv1.BusPacket{
		AuthToken: strings.Repeat("x", maximumHandshakePacketBytes+1),
		Payload: &agentv1.BusPacket_WorkerHandshakeChallenge{
			WorkerHandshakeChallenge: &agentv1.WorkerHandshakeChallenge{ChallengeId: "challenge-1"},
		},
	}
	return []opaqueHandshakeCase{
		{name: "empty"},
		{name: "malformed", request: model.RawRequest{0xff}},
		{name: "oversized", request: make(model.RawRequest, maximumHandshakePacketBytes+1)},
		{name: "service error", request: request, serviceErr: errors.New("redis token=secret"), wantCalls: 1},
		{name: "nil response", request: request, wantCalls: 1},
		{name: "wrong response phase", request: request, response: wrongPhase, wantCalls: 1},
		{name: "oversized response", request: request, response: oversized, wantCalls: 1},
	}
}

func assertOpaqueChallengeRejection(t *testing.T, bus *fakeHandshakeResponder, service *fakeHandshakeProtocolService, test opaqueHandshakeCase) {
	t.Helper()
	response, err := bus.invoke(context.Background(), WorkerHandshakeChallengeSubject, test.request)
	if response != nil || !errors.Is(err, errHandshakeRequestRejected) {
		t.Fatalf("response=%x error=%v, want opaque rejection", response, err)
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "redis") {
		t.Fatalf("error leaked service detail: %v", err)
	}
	challengeCalls, _ := service.counts()
	if challengeCalls != test.wantCalls {
		t.Fatalf("service calls = %d, want %d", challengeCalls, test.wantCalls)
	}
}

func TestHandshakeSubscriberAuthenticateFailureIsOpaque(t *testing.T) {
	service := &fakeHandshakeProtocolService{authErr: errors.New("token=secret")}
	bus := &fakeHandshakeResponder{}
	subscriber, err := NewHandshakeSubscriber(bus, service)
	if err != nil {
		t.Fatalf("new subscriber: %v", err)
	}
	if err := subscriber.Start(); err != nil {
		t.Fatalf("start subscriber: %v", err)
	}
	defer func() {
		if err := subscriber.Close(); err != nil {
			t.Errorf("close subscriber: %v", err)
		}
	}()
	request := marshalHandshakeRaw(t, &agentv1.BusPacket{
		Payload: &agentv1.BusPacket_WorkerHandshakeAuthenticate{
			WorkerHandshakeAuthenticate: &agentv1.WorkerHandshakeAuthenticate{},
		},
	})
	response, err := bus.invoke(context.Background(), WorkerHandshakeAuthenticateSubject, request)
	if response != nil || !errors.Is(err, errHandshakeRequestRejected) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("response=%x error=%v, want opaque rejection", response, err)
	}
	if _, authCalls := service.counts(); authCalls != 1 {
		t.Fatalf("authenticate calls = %d, want 1", authCalls)
	}
}
