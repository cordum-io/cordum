//go:build handshakeinterop

package handshakeinterop

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/scheduler"
)

type storedSessionRecord struct {
	JTI        string `json:"jti"`
	ExpUnix    int64  `json:"exp_unix"`
	WorkerID   string `json:"worker_id"`
	AgentID    string `json:"agent_id"`
	Tenant     string `json:"tenant"`
	Audience   string `json:"aud"`
	ProofKeyID string `json:"proof_key_id"`
	SDKVersion string `json:"sdk_ver"`
}

func TestInstalledSessionTransitionRequiresExactBoundState(t *testing.T) {
	identity := &interopIdentity{
		workerID: "worker", agentID: "agent", tenantID: "tenant",
		keyID: "proof", sdkVersion: "cap-go/v2",
	}
	active, err := json.Marshal(storedSessionRecord{
		JTI: "fresh", ExpUnix: time.Now().Add(time.Minute).Unix(), WorkerID: identity.workerID,
		AgentID: identity.agentID, Tenant: identity.tenantID, Audience: scheduler.WorkerHandshakeAudience,
		ProofKeyID: identity.keyID, SDKVersion: identity.sdkVersion,
	})
	if err != nil {
		t.Fatalf("marshal active record: %v", err)
	}
	before := redisState{"session:worker:foreign": []byte("foreign")}
	after := redisState{
		"session:worker:foreign":                         []byte("foreign"),
		activeSessionKey(identity):                       active,
		revokedSessionPrefix(identity) + "prior-session": []byte("1"),
	}
	if err := validateInstalledSessionTransition(identity, before, after, time.Now()); err != nil {
		t.Fatalf("valid transition rejected: %v", err)
	}
	delete(after, revokedSessionPrefix(identity)+"prior-session")
	if err := validateInstalledSessionTransition(identity, before, after, time.Now()); err == nil {
		t.Fatal("transition without prior-token revocation accepted")
	}
}

func validateInstalledSessionTransition(identity *interopIdentity, before, after redisState, now time.Time) error {
	if identity == nil || len(after) != len(before)+2 {
		return fmt.Errorf("state count %d -> %d, want exactly two new keys", len(before), len(after))
	}
	for key, value := range before {
		if !bytes.Equal(value, after[key]) {
			return fmt.Errorf("existing authority changed at %s", key)
		}
	}
	activeKey := activeSessionKey(identity)
	record, err := decodeStoredSession(after[activeKey])
	if err != nil {
		return err
	}
	if err := validateStoredIdentity(record, identity, now); err != nil {
		return err
	}
	revocationPrefix := revokedSessionPrefix(identity)
	revocations := 0
	for key, value := range after {
		if _, existed := before[key]; existed || key == activeKey {
			continue
		}
		if !strings.HasPrefix(key, revocationPrefix) || string(value) != "1" ||
			strings.TrimPrefix(key, revocationPrefix) == record.JTI {
			return fmt.Errorf("unexpected new authority key %s", key)
		}
		revocations++
	}
	if revocations != 1 {
		return fmt.Errorf("revocation keys=%d want=1", revocations)
	}
	return nil
}

func decodeStoredSession(value []byte) (storedSessionRecord, error) {
	var record storedSessionRecord
	if len(value) == 0 {
		return record, fmt.Errorf("active Redis session missing")
	}
	if err := json.Unmarshal(value, &record); err != nil {
		return record, fmt.Errorf("decode active Redis session: %w", err)
	}
	return record, nil
}

func validateStoredIdentity(record storedSessionRecord, identity *interopIdentity, now time.Time) error {
	if record.JTI == "" || record.ExpUnix <= now.Unix() || record.WorkerID != identity.workerID ||
		record.AgentID != identity.agentID || record.Tenant != identity.tenantID ||
		record.Audience != scheduler.WorkerHandshakeAudience || record.ProofKeyID != identity.keyID ||
		record.SDKVersion != identity.sdkVersion {
		return fmt.Errorf("active Redis session binding mismatch: %+v", record)
	}
	return nil
}
