package scheduler

import (
	"testing"

	"github.com/cordum/cordum/core/auth/servicetoken"
)

func TestHandshakeServiceReservedServiceIdentityRejected(t *testing.T) {
	for _, identity := range []string{servicetoken.IdentityScheduler, servicetoken.IdentityGateway, servicetoken.IdentityWorkflow} {
		t.Run(identity, func(t *testing.T) {
			fixture := newProtocolHandshakeFixture(t)
			defer fixture.cleanup()
			packet := protocolChallengeRequest(t, fixture, issuePurpose())
			packet.SenderId = identity
			packet.GetWorkerHandshakeChallengeRequest().WorkerId = identity
			resignChallengeRequest(t, packet, fixture.workerKey)
			assertChallengeError(t, fixture, packet, authenticationFailedReason(), "reserved")
			assertNoVictimSession(t, fixture)
		})
	}
}
