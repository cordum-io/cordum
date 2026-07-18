package scheduler

import (
	"context"
	"errors"
	"testing"

	capsdk "github.com/cordum-io/cap/v2/sdk/go"
)

func TestNewHandshakeSubscriberRequiresLiveDependencies(t *testing.T) {
	service := &fakeHandshakeProtocolService{}
	bus := &fakeHandshakeResponder{}
	var nilBus *fakeHandshakeResponder
	var nilService *fakeHandshakeProtocolService
	tests := []struct {
		name    string
		bus     handshakeRawResponder
		service handshakeProtocolService
	}{
		{name: "nil bus", service: service},
		{name: "typed nil bus", bus: nilBus, service: service},
		{name: "nil service", bus: bus},
		{name: "typed nil service", bus: bus, service: nilService},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewHandshakeSubscriber(test.bus, test.service); err == nil {
				t.Fatal("NewHandshakeSubscriber error = nil")
			}
		})
	}
	if err := (&HandshakeSubscriber{}).Start(); err == nil {
		t.Fatal("zero-value HandshakeSubscriber.Start error = nil")
	}
}

func TestHandshakeSubscriberRegistersOnlyAuthenticatedHAEndpoints(t *testing.T) {
	bus := &fakeHandshakeResponder{}
	service := &fakeHandshakeProtocolService{}
	subscriber, err := NewHandshakeSubscriber(bus, service)
	if err != nil {
		t.Fatalf("new subscriber: %v", err)
	}
	if err := subscriber.Start(); err != nil {
		t.Fatalf("start subscriber: %v", err)
	}
	registrations := assertAuthenticatedRegistrations(t, bus)
	assertLegacySubjectsUnregistered(t, bus, service)
	if err := subscriber.Start(); !errors.Is(err, errHandshakeSubscriberStarted) {
		t.Fatalf("second Start error = %v", err)
	}
	if err := subscriber.Close(); err != nil {
		t.Fatalf("close subscriber: %v", err)
	}
	for _, registration := range registrations {
		if got := registration.sub.unsubscribeCalls(); got != 1 {
			t.Fatalf("%s unsubscribe calls = %d, want 1", registration.subject, got)
		}
	}
	if err := subscriber.Close(); err != nil {
		t.Fatalf("idempotent close: %v", err)
	}
}

func assertAuthenticatedRegistrations(t *testing.T, bus *fakeHandshakeResponder) []fakeHandshakeRegistration {
	t.Helper()
	registrations := bus.snapshot()
	if len(registrations) != 2 {
		t.Fatalf("registrations = %d, want 2", len(registrations))
	}
	want := map[string]bool{
		WorkerHandshakeChallengeSubject: false, WorkerHandshakeAuthenticateSubject: false,
	}
	for _, registration := range registrations {
		if _, ok := want[registration.subject]; !ok {
			t.Fatalf("unexpected handshake subject %q", registration.subject)
		}
		if registration.queue != schedulerQueue || registration.handler == nil {
			t.Fatalf("invalid registration for %q: queue=%q handler_nil=%t", registration.subject, registration.queue, registration.handler == nil)
		}
		want[registration.subject] = true
	}
	for subject, seen := range want {
		if !seen {
			t.Fatalf("authenticated subject %q not registered", subject)
		}
	}
	return registrations
}

func assertLegacySubjectsUnregistered(t *testing.T, bus *fakeHandshakeResponder, service *fakeHandshakeProtocolService) {
	t.Helper()
	legacySubjects := []string{capsdk.WorkerHandshakeSubject, capsdk.WorkerHandshakeRenewSubject, capsdk.SubjectHandshake}
	for _, legacy := range legacySubjects {
		if _, err := bus.invoke(context.Background(), legacy, nil); !errors.Is(err, errNoHandshakeRegistration) {
			t.Fatalf("legacy subject %q has a responder: %v", legacy, err)
		}
	}
	if challengeCalls, authCalls := service.counts(); challengeCalls != 0 || authCalls != 0 {
		t.Fatalf("legacy subjects reached minting service: challenge=%d authenticate=%d", challengeCalls, authCalls)
	}
}

func TestHandshakeSubscriberCleansPartialStartAndCloseFailures(t *testing.T) {
	bus := &fakeHandshakeResponder{
		failSubject: WorkerHandshakeAuthenticateSubject, subscribeErr: errors.New("subscribe failed"),
	}
	subscriber, err := NewHandshakeSubscriber(bus, &fakeHandshakeProtocolService{})
	if err != nil {
		t.Fatalf("new subscriber: %v", err)
	}
	if err := subscriber.Start(); err == nil {
		t.Fatal("Start error = nil")
	}
	registrations := bus.snapshot()
	if len(registrations) != 1 || registrations[0].sub.unsubscribeCalls() != 1 {
		t.Fatalf("partial registration was not cleaned: %+v", registrations)
	}
	bus.failSubject = ""
	bus.unsubscribeErr = errors.New("unsubscribe failed")
	if err := subscriber.Start(); err != nil {
		t.Fatalf("retry Start: %v", err)
	}
	if err := subscriber.Close(); err == nil {
		t.Fatal("Close error = nil, want unsubscribe failure")
	}
	if err := subscriber.Close(); err != nil {
		t.Fatalf("second Close error = %v", err)
	}
}
