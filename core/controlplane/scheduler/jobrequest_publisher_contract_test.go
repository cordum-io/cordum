package scheduler

import (
	"os"
	"strings"
	"testing"
)

func TestProductionJobRequestPublishersAttachServiceAuthority(t *testing.T) {
	tests := []struct {
		path, marker string
		want         int
	}{
		{"../gateway/handlers_approvals.go", "attachServiceToken(packet)", 2},
		{"../gateway/handlers_dlq.go", "attachServiceToken(packet)", 1},
		{"../gateway/handlers_grpc.go", "attachServiceToken(packet)", 1},
		{"../gateway/handlers_jobs.go", "attachServiceToken(packet)", 2},
		{"engine.go", "attachServiceToken(packet)", 1},
		{"saga.go", "sessionMiddleware.MintServiceToken(defaultSenderID)", 1},
		{"../../workflow/engine_job.go", "serviceTokenMinter()", 1},
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
				const publisher = "&pb.BusPacket_JobRequest"
				offset := strings.Index(source[cursor:], publisher)
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
				cursor = start + len(publisher)
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
