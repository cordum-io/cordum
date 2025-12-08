package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	pb "github.com/yaront1111/cortex-os/core/pkg/pb/v1"
)

type fakeBus struct {
	subs map[string]func(*pb.BusPacket)
}

func newFakeBus() *fakeBus {
	return &fakeBus{subs: make(map[string]func(*pb.BusPacket))}
}

func (b *fakeBus) Publish(subject string, packet *pb.BusPacket) error {
	for sub, handler := range b.subs {
		if sub == subject {
			handler(packet)
			continue
		}
		if strings.HasSuffix(sub, ">") {
			prefix := strings.TrimSuffix(sub, ">")
			if strings.HasPrefix(subject, prefix) {
				handler(packet)
			}
		}
	}
	return nil
}

func (b *fakeBus) Subscribe(subject, queue string, handler func(*pb.BusPacket)) error {
	b.subs[subject] = handler
	return nil
}

func TestHandleGetWorkersUsesHeartbeatSnapshot(t *testing.T) {
	bus := newFakeBus()
	s := &server{
		bus:      bus,
		workers:  make(map[string]*pb.Heartbeat),
		clients:  make(map[*websocket.Conn]chan *pb.BusPacket),
		eventsCh: make(chan *pb.BusPacket, 10),
	}
	s.startBusTaps()
	defer close(s.eventsCh)

	// Simulate a heartbeat
	hb := &pb.Heartbeat{
		WorkerId: "w1",
		Type:     "cpu",
		Pool:     "echo",
	}
	bus.Publish("sys.heartbeat.echo", &pb.BusPacket{Payload: &pb.BusPacket_Heartbeat{Heartbeat: hb}})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/workers", nil)

	s.handleGetWorkers(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var out []*pb.Heartbeat
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out) != 1 || out[0].WorkerId != "w1" {
		t.Fatalf("unexpected workers response: %#v", out)
	}
}
