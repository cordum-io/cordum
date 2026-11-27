package fake

import (
	"testing"

	idm "github.com/cordum/cordum/tests/identity"
)

var run = idm.Run

func TestAudienceMatrixWorkload(t *testing.T) { idm.Run(t, idm.WorkloadAPI, nil) }

func TestAudienceMatrixFirst(t *testing.T) { idm.Run(t, idm.HTTPAPI, nil) }

func TestAudienceMatrixSecond(t *testing.T) { idm.Run(t, idm.HTTPAPI, nil) }

func TestAudienceMatrixEach(t *testing.T) {
	for _, e := range idm.Entries {
		idm.Run(t, e, nil)
	}
}

func check(t *testing.T) { idm.Run(t, idm.EdgeAPI, nil) }
