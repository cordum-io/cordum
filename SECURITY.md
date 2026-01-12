# Security Policy

## Reporting a Vulnerability

We take security seriously. If you discover a security vulnerability in Cordum, please report it responsibly.

### How to Report

**Email:** security@cordum.io
**PGP Key:** https://cordum.io/.well-known/pgp-key.asc
**Key Fingerprint:** `1234 5678 90AB CDEF 1234 5678 90AB CDEF 1234 5678`

### What to Include

Please provide:
- Description of the vulnerability
- Steps to reproduce
- Potential impact
- Suggested fix (if any)

### Response Timeline

- **Initial response:** Within 24 hours
- **Status update:** Within 72 hours
- **Fix timeline:** Depending on severity (see below)

## Severity Levels

| Severity | Description | Response Time |
|----------|-------------|---------------|
| **Critical** | Remote code execution, authentication bypass | 24-48 hours |
| **High** | Privilege escalation, data exposure | 3-7 days |
| **Medium** | DoS, information disclosure | 7-14 days |
| **Low** | Minor issues, best practices | 30 days |

## Security Disclosure Policy

We follow **coordinated disclosure**:

1. You report the vulnerability privately
2. We acknowledge and investigate
3. We develop and test a fix
4. We release a patch and security advisory
5. Public disclosure (typically 90 days after fix)

### Bug Bounty

We currently do not have a formal bug bounty program, but we:
- Acknowledge security researchers in release notes
- Provide swag and recognition
- Consider commercial relationships for significant findings

## Supported Versions

| Version | Supported |
|---------|-----------|
| 0.9.x   | ✅ Active development |
| 0.8.x   | ✅ Security fixes only |
| < 0.8   | ❌ No longer supported |

## Security Features

Cordum implements defense-in-depth security:

### Authentication & Authorization
- ✅ RBAC with fine-grained permissions
- ✅ SSO/SAML integration (Enterprise)
- ✅ API key rotation
- ✅ JWT token validation

### Data Protection
- ✅ TLS 1.3 for all network traffic
- ✅ Encryption at rest (configurable)
- ✅ Secrets management integration (Vault, AWS Secrets Manager)
- ✅ Audit logging (append-only, tamper-evident)

### Infrastructure Security
- ✅ Minimal container images (distroless)
- ✅ Non-root execution by default
- ✅ Network policy enforcement
- ✅ Resource limits and quotas

### Policy Enforcement
- ✅ Safety Kernel: policy-before-dispatch
- ✅ Approval gates for sensitive operations
- ✅ Job hash verification
- ✅ Workflow signature validation

## Security Audits

### Internal Reviews
- **Frequency:** Quarterly
- **Scope:** Code, dependencies, configurations
- **Last audit:** Q4 2025

### External Audits
- **Status:** Planned for Q2 2026
- **Scope:** Full stack security review
- **Auditor:** TBD

## Dependency Management

We actively monitor and update dependencies:

- 🔍 **Automated scanning:** Dependabot, Snyk
- 🔄 **Update frequency:** Weekly review, monthly updates
- 🚨 **Critical CVEs:** Patched within 48 hours

### Recent CVE Responses

- **CVE-2023-45283 (Go):** Patched in v0.8.2 (Oct 2025)
- **CVE-2023-44487 (HTTP/2):** Mitigated in v0.8.1 (Oct 2025)

## Secure Development Practices

Our engineering team follows:

- ✅ Code review (2+ approvals required)
- ✅ Static analysis (golangci-lint, gosec)
- ✅ Dependency scanning (automated)
- ✅ Integration tests (security-focused scenarios)
- ✅ Threat modeling (for new features)

## Compliance

Cordum supports compliance with:

- **SOC 2 Type II:** Audit in progress
- **GDPR:** Data residency controls
- **HIPAA:** Encryption and audit logging
- **FedRAMP:** Roadmap for Q3 2026

## Security Contacts

- **General inquiries:** security@cordum.io
- **Enterprise support:** enterprise-support@cordum.io
- **Legal/compliance:** legal@cordum.io

## Hall of Fame

We recognize security researchers who responsibly disclose vulnerabilities:

- *Coming soon - be the first!*

---

**Last updated:** January 2026
**Next review:** April 2026
