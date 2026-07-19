package main

import (
	"os"
	"strings"
	"testing"
)

func TestSecurityPolicyTruthContract(t *testing.T) {
	policyBytes, err := os.ReadFile("SECURITY.md")
	if err != nil {
		t.Fatalf("read SECURITY.md: %v", err)
	}
	policy := string(policyBytes)

	required := []string{
		"https://github.com/cordum-io/cordum/security/advisories/new",
		"https://cordum.io/.well-known/security.txt",
		"| 1.1.x | Active |",
		"| 1.0.x | Security fixes only |",
		"| Earlier than 1.0 | Not supported |",
		"Our response times are targets, not service-level agreements.",
		"Cordum does not currently publish a PGP key for vulnerability reports.",
	}
	assertPolicyContains(t, policy, required)

	unsupported := []string{
		"pgp-key.asc", "1234 5678 90AB CDEF", "**PGP Key:**", "**Key Fingerprint:**",
		"security@cordum.io",
		"enterprise-support@cordum.io", "legal@cordum.io",
		"**Initial response:**", "**Status update:**", "**Fix timeline:**", "## Severity Levels",
		"Within 24 hours", "Within 72 hours", "24-48 hours", "3-7 days", "7-14 days",
		"30 days", "Patched within 48 hours", "typically 90 days after fix", "## Bug Bounty",
		"release notes", "Provide swag", "commercial relationships", "| 0.9.x", "| 0.8.x", "| < 0.8",
		"## Security Features", "RBAC with fine-grained permissions", "SSO/SAML integration",
		"API key rotation", "JWT token validation", "TLS 1.3 for all network traffic",
		"Encryption at rest", "Secrets management integration", "tamper-evident",
		"distroless", "Workflow signature validation", "## Security Audits", "Quarterly",
		"Last audit", "Planned for Q2 2026", "## Dependency Management", "Weekly review, monthly updates",
		"### Recent CVE Responses", "Snyk", "CVE-2023-45283",
		"CVE-2023-44487", "2+ approvals required", "## Compliance", "SOC 2 Type II",
		"## Secure Development Practices", "golangci-lint, gosec", "Threat modeling",
		"GDPR", "HIPAA", "FedRAMP", "## Hall of Fame", "Next review: April 2026",
	}
	assertPolicyOmits(t, policy, unsupported)
}

func assertPolicyContains(t *testing.T, policy string, phrases []string) {
	t.Helper()
	for _, phrase := range phrases {
		if !strings.Contains(policy, phrase) {
			t.Errorf("SECURITY.md must contain %q", phrase)
		}
	}
}

func assertPolicyOmits(t *testing.T, policy string, phrases []string) {
	t.Helper()
	lowerPolicy := strings.ToLower(policy)
	for _, phrase := range phrases {
		if strings.Contains(lowerPolicy, strings.ToLower(phrase)) {
			t.Errorf("SECURITY.md contains unsupported claim %q", phrase)
		}
	}
}
