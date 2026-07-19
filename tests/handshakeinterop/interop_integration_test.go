//go:build handshakeinterop

package handshakeinterop

import "testing"

func TestAuthenticatedWorkerHandshakeInterop(t *testing.T) {
	harness := newInteropHarness(t)
	defer harness.Close()

	t.Run("installed SDKs issue and renew", harness.TestInstalledClients)
	t.Run("required negatives fail in every SDK", harness.TestNegativeClients)
	t.Run("legacy unsigned JSON has no responder", harness.TestLegacySubjects)
	t.Run("renew supersedes the prior bound session", harness.TestSessionSupersession)
	t.Run("two replicas share Redis authority", harness.TestReplicaUse)
	t.Run("concurrent replay mints exactly once", harness.TestConcurrentReplay)
	t.Run("responders survive broker restart", harness.TestBrokerRestart)
}
