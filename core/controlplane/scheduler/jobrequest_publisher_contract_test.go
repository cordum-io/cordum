package scheduler

import (
	"os"
	"strings"
	"testing"
)

func TestProductionJobRequestPublishersAttachServiceAuthority(t *testing.T) {
	tests := []struct {
		path, publisher, marker string
		want                    int
	}{
		// The gateway handlers build JobRequest BusPackets through the shared
		// jobRequestPacket() helper (core/controlplane/gateway/job_identity.go)
		// rather than the raw &pb.BusPacket_JobRequest{...} literal, so the
		// publisher trigger for these files is the helper call itself.
		{"../gateway/handlers_approvals.go", "jobRequestPacket(", "attachServiceToken(packet)", 2},
		{"../gateway/handlers_dlq.go", "jobRequestPacket(", "attachServiceToken(packet)", 1},
		{"../gateway/handlers_grpc.go", "jobRequestPacket(", "attachServiceToken(packet)", 1},
		{"../gateway/handlers_jobs.go", "jobRequestPacket(", "attachServiceToken(packet)", 2},
		{"engine.go", "&pb.BusPacket_JobRequest", "attachServiceToken(packet)", 1},
		{"saga.go", "&pb.BusPacket_JobRequest", "sessionMiddleware.MintServiceToken(defaultSenderID)", 1},
		{"../../workflow/engine_job.go", "&pb.BusPacket_JobRequest", "serviceTokenMinter()", 1},
	}
	total := 0
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			raw, err := os.ReadFile(test.path)
			if err != nil {
				t.Fatal(err)
			}
			source := string(raw)
			cursor, found := 0, 0
			for {
				offset := strings.Index(source[cursor:], test.publisher)
				if offset < 0 {
					break
				}
				start := cursor + offset
				end := start + 1200
				if end > len(source) {
					end = len(source)
				}
				if !strings.Contains(source[start:end], test.marker) {
					t.Fatalf("JobRequest publisher at byte %d lacks %q", start, test.marker)
				}
				found++
				cursor = start + len(test.publisher)
			}
			if found != test.want {
				t.Fatalf("JobRequest publishers=%d, want %d", found, test.want)
			}
			total += found
		})
	}
	if total != 9 {
		t.Fatalf("authenticated production JobRequest publishers=%d, want 9", total)
	}
}
