package capsdk

import (
	"strings"
	"testing"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestValidateBusPacket(t *testing.T) {
	validPayload := &pb.BusPacket_JobRequest{JobRequest: &pb.JobRequest{JobId: "j"}}
	cases := []struct {
		name        string
		pkt         *pb.BusPacket
		wantErr     bool
		errContains string
	}{
		{
			name:        "nil_packet",
			pkt:         nil,
			wantErr:     true,
			errContains: "nil packet",
		},
		{
			name:        "missing_trace_id",
			pkt:         &pb.BusPacket{SenderId: "s", Payload: validPayload},
			wantErr:     true,
			errContains: "trace_id",
		},
		{
			name:        "missing_sender_id",
			pkt:         &pb.BusPacket{TraceId: "t", Payload: validPayload},
			wantErr:     true,
			errContains: "sender_id",
		},
		{
			name:        "missing_payload",
			pkt:         &pb.BusPacket{TraceId: "t", SenderId: "s"},
			wantErr:     true,
			errContains: "payload",
		},
		{
			name:        "whitespace_only_trace_id",
			pkt:         &pb.BusPacket{TraceId: "   ", SenderId: "s", Payload: validPayload},
			wantErr:     true,
			errContains: "trace_id",
		},
		{
			name:    "valid",
			pkt:     &pb.BusPacket{TraceId: "t", SenderId: "s", Payload: validPayload},
			wantErr: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateBusPacket(tc.pkt)
			if tc.wantErr {
				if err == nil {
					t.Fatal("err = nil, want non-nil")
				}
				if !strings.Contains(err.Error(), tc.errContains) {
					t.Fatalf("err = %q, want substring %q", err.Error(), tc.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
		})
	}
}
