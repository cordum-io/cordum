package contract

import (
	"path/filepath"
	"slices"
	"testing"
)

// Cross-checks between docs/contract/claims.yaml and docs/contract/invariants.yaml
// for the M0 exit gate. The enforced-cell test link is matrix.go validateRefTests.

func contractFiles(t *testing.T) (string, *ClaimRegister, *InvariantCatalog) {
	t.Helper()
	root, cat := invariantsFixture(t)
	reg, err := LoadClaimRegister(filepath.Join(root, "docs/contract/claims.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return root, reg, cat
}

// verifiedOnViolated lists "claim:invariant" for each verified claim that names
// an invariant whose catalog status is violated.
func verifiedOnViolated(reg *ClaimRegister, cat *InvariantCatalog) []string {
	status := map[string]string{}
	for _, inv := range cat.Invariants {
		status[inv.ID] = inv.Status
	}
	var bad []string
	for _, c := range reg.Claims {
		for _, id := range deref(c.Invariants) {
			if c.Status == "verified" && status[id] == "violated" {
				bad = append(bad, c.ID+":"+id)
			}
		}
	}
	return bad
}

// unresolvedInvariants lists "claim:invariant" for each claim invariant id that
// is not in the catalog, through the catalog's claim-invariant-unknown rule.
func unresolvedInvariants(root string, reg *ClaimRegister, cat *InvariantCatalog) []string {
	var bad []string
	for _, is := range cat.Validate(root, reg) {
		if is.Rule == "claim-invariant-unknown" {
			bad = append(bad, is.ID+":"+is.Detail)
		}
	}
	return bad
}

func fixtureClaim(id, status string, invariants ...string) Claim {
	return Claim{ID: id, Status: status, Invariants: &invariants}
}

// fixtureCatalog does not depend on the repository's current statuses, so the
// fixtures keep biting after M1 fixes change them.
var fixtureCatalog = &InvariantCatalog{Invariants: []Invariant{{ID: "I01", Status: "violated"}, {ID: "I02", Status: "holds"}}}

func TestCrossCheckVerifiedClaimsAvoidViolatedInvariants(t *testing.T) {
	_, reg, cat := contractFiles(t)
	if got := verifiedOnViolated(reg, cat); len(got) != 0 {
		t.Errorf("repository: verified claims on violated invariants: %v", got)
	}
	for _, tc := range []struct {
		name  string
		claim Claim
		want  []string
	}{
		{"verified_on_violated", fixtureClaim("CLM-901", "verified", "I02", "I01"), []string{"CLM-901:I01"}},
		{"verified_on_holds", fixtureClaim("CLM-902", "verified", "I02"), nil},
		{"supported_on_violated", fixtureClaim("CLM-903", "supported", "I01"), nil},
	} {
		got := verifiedOnViolated(&ClaimRegister{Claims: []Claim{tc.claim}}, fixtureCatalog)
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestCrossCheckClaimInvariantIDsResolve(t *testing.T) {
	root, reg, cat := contractFiles(t)
	if got := unresolvedInvariants(root, reg, cat); len(got) != 0 {
		t.Errorf("repository: claim invariant ids missing from invariants.yaml: %v", got)
	}
	for _, tc := range []struct {
		name  string
		claim Claim
		want  []string
	}{
		{"unknown_id", fixtureClaim("CLM-904", "supported", "I01", "I99"), []string{"CLM-904:I99"}},
		{"known_ids", fixtureClaim("CLM-905", "verified", "I01", "I02"), nil},
	} {
		got := unresolvedInvariants(root, &ClaimRegister{Claims: []Claim{tc.claim}}, fixtureCatalog)
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}
