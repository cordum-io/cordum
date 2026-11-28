package bus

import (
	"context"
	"fmt"
	"testing"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// A publish from another connection can reach the server before a subscription
// that is only queued in this client's buffer, and core NATS then drops it. So
// the server must already know the subscription when the subscribe call returns.
func TestNatsBusCoreSubscribeRegisteredOnReturn(t *testing.T) {
	ns := startTestNATSServer(t, false)
	b := newTestNatsBus(t, ns, false)
	plain := func(subject, queue string) error {
		return b.Subscribe(subject, queue, func(*pb.BusPacket) error { return nil })
	}
	withContext := func(subject, queue string) error {
		return b.SubscribeWithContext(subject, queue, func(context.Context, *pb.BusPacket) error { return nil })
	}
	cases := []struct {
		name, queue string
		subscribe   func(subject, queue string) error
	}{
		{"Subscribe", "", plain},
		{"SubscribeQueue", "workers", plain},
		{"SubscribeWithContext", "", withContext},
		{"SubscribeWithContextQueue", "workers", withContext},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for n := range 10 {
				subject := fmt.Sprintf("test.registered.%s.%d", tc.name, n)
				if err := tc.subscribe(subject, tc.queue); err != nil {
					t.Fatalf("subscribe %s: %v", subject, err)
				}
				if !ns.GlobalAccount().SubscriptionInterest(subject) {
					t.Fatalf("server has no interest in %s when subscribe returned", subject)
				}
			}
		})
	}
}
