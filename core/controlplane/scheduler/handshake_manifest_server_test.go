package scheduler

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	capsdk "github.com/cordum-io/cap/v2/sdk/go"
)

type serverVector struct {
	ID       string            `json:"id"`
	Mutation serverMutation    `json:"mutation"`
	Expected serverExpectation `json:"expected"`
}

type serverMutation struct {
	Kind  string          `json:"kind"`
	Path  string          `json:"path"`
	Value json.RawMessage `json:"value"`
}

type serverExpectation struct {
	Accepted       *bool  `json:"accepted"`
	Reason         string `json:"reason"`
	InstallCount   *int   `json:"install_count"`
	MintCount      *int   `json:"mint_count"`
	AcceptedCount  *int   `json:"accepted_count"`
	RejectedCount  *int   `json:"rejected_count"`
	ChallengeCount *int   `json:"challenge_count"`
}

type serverOutcome struct {
	Accepted, InstallCount, MintCount            bool
	AcceptedTotal, RejectedTotal, ChallengeTotal int
	Reason                                       string
}

type serverVectorHandler func(*testing.T, serverVector) serverOutcome

var serverVectorHandlers = map[string]serverVectorHandler{
	"replay_exact": runReplayVector, "replay_concurrent": runReplayVector,
	"skew_positive_boundary": runSkewVector, "skew_negative_boundary": runSkewVector,
	"skew_positive_over_1ns": runSkewVector, "skew_negative_over_1ns": runSkewVector,
	"audience_wrong": runAudienceVector, "challenge_expired": runExpiredChallengeVector,
	"response_request_id_mismatch":   runResponseBindingVector,
	"response_trace_mismatch":        runResponseBindingVector,
	"response_challenge_id_mismatch": runResponseBindingVector,
	"token_worker_mismatch":          runTokenClaimVector, "token_agent_mismatch": runTokenClaimVector,
	"token_tenant_mismatch": runTokenClaimVector, "token_audience_mismatch": runTokenClaimVector,
	"token_proof_key_mismatch": runTokenClaimVector,
	"renew_a_to_b":             runRenewBindingVector, "renew_superseded_token": runRenewBindingVector,
	"state_store_unavailable": runUnavailableStoreVector,
}

func TestCAPManifestServerVectorsExecuteAgainstScheduler(t *testing.T) {
	vectors := loadCAPServerVectors(t)
	if len(vectors) != len(serverVectorHandlers) {
		t.Fatalf("server vectors=%d handlers=%d", len(vectors), len(serverVectorHandlers))
	}
	seen := make(map[string]bool, len(vectors))
	for _, vector := range vectors {
		vector := vector
		t.Run(vector.ID, func(t *testing.T) {
			handler, ok := serverVectorHandlers[vector.ID]
			if !ok {
				t.Fatalf("CAP server vector %q has no scheduler handler", vector.ID)
			}
			seen[vector.ID] = true
			assertServerExpectation(t, vector.Expected, handler(t, vector))
		})
	}
	for id := range serverVectorHandlers {
		if !seen[id] {
			t.Fatalf("scheduler handler %q has no CAP manifest vector", id)
		}
	}
}

func loadCAPServerVectors(t *testing.T) []serverVector {
	t.Helper()
	pc := reflect.ValueOf(capsdk.ValidateWorkerTrustPacket).Pointer()
	file, _ := runtime.FuncForPC(pc).FileLine(pc)
	path := filepath.Join(filepath.Dir(file), "..", "..", "spec", "conformance", "fixtures", "handshake", "manifest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read CAP handshake manifest: %v", err)
	}
	var manifest struct {
		Negative []serverVector `json:"negative_vectors"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode CAP handshake manifest: %v", err)
	}
	if len(manifest.Negative) != 38 {
		t.Fatalf("CAP negative vector count=%d want=38", len(manifest.Negative))
	}
	vectors := make([]serverVector, 0, len(serverVectorHandlers))
	for _, vector := range manifest.Negative {
		if _, ok := serverVectorHandlers[vector.ID]; ok {
			vectors = append(vectors, vector)
		}
	}
	return vectors
}

func assertServerExpectation(t *testing.T, want serverExpectation, got serverOutcome) {
	t.Helper()
	if got.Reason != want.Reason {
		t.Fatalf("reason=%s want=%s", got.Reason, want.Reason)
	}
	if want.Accepted != nil && got.Accepted != *want.Accepted {
		t.Fatalf("accepted=%v want=%v", got.Accepted, *want.Accepted)
	}
	assertOptionalCount(t, "install", want.InstallCount, boolCount(got.InstallCount))
	assertOptionalCount(t, "mint", want.MintCount, boolCount(got.MintCount))
	assertOptionalCount(t, "accepted", want.AcceptedCount, got.AcceptedTotal)
	assertOptionalCount(t, "rejected", want.RejectedCount, got.RejectedTotal)
	assertOptionalCount(t, "challenge", want.ChallengeCount, got.ChallengeTotal)
}

func assertOptionalCount(t *testing.T, name string, want *int, got int) {
	t.Helper()
	if want != nil && got != *want {
		t.Fatalf("%s_count=%d want=%d", name, got, *want)
	}
}

func boolCount(value bool) int {
	if value {
		return 1
	}
	return 0
}
