// Package tlsutil provides shared helpers for loading and verifying TLS
// material in Cordum services.
//
// The single use case today is VerifyChain, which every client-TLS loader
// (NATS, Redis, safety-kernel gRPC, context-engine gRPC) should call AFTER
// tls.LoadX509KeyPair succeeds. It surfaces a rich, actionable error
// when the client cert and the configured CA don't form a valid chain —
// the #1 cause of "tls: certificate required" mystery outages during
// redeploys where CA/client certs drift out of sync.
package tlsutil

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"time"
)

// ChainRole distinguishes client vs server extended key usage. The verifier
// fails early if the cert was issued for the wrong side — a server cert
// presented by a client wouldn't hit the "certificate required" error from
// a mutual-TLS-required server, but it would fail a server-side key-usage
// check downstream; surface the mismatch at load time instead.
type ChainRole int

const (
	// RoleClient verifies ExtKeyUsageClientAuth (NATS/Redis/gRPC client certs).
	RoleClient ChainRole = iota
	// RoleServer verifies ExtKeyUsageServerAuth (not used by cordum clients yet,
	// but symmetrical for future serving code).
	RoleServer
)

// VerifyChain loads the PEM cert at certPath and the PEM CA at caPath, then
// verifies the cert chains to the CA with the given key usage. On mismatch
// the returned error includes:
//   - file paths
//   - subject + issuer DNs of both cert and CA (catches CA-rotated-without-client)
//   - NotBefore/NotAfter of both (catches one-was-regenerated-newer)
//   - an actionable remediation hint pointing at cordumctl generate-certs
//
// The caller's log surface (slog.Error) carries this error verbatim so operators
// see exactly why the stack isn't starting, instead of the opaque "tls:
// certificate required" from nats-server / redis-server.
func VerifyChain(certPath, caPath string, role ChainRole) error {
	certPEM, err := os.ReadFile(certPath) // #nosec G304 -- operator-configured path.
	if err != nil {
		return fmt.Errorf("tlsutil: read cert %q: %w", certPath, err)
	}
	caPEM, err := os.ReadFile(caPath) // #nosec G304 -- operator-configured path.
	if err != nil {
		return fmt.Errorf("tlsutil: read ca %q: %w", caPath, err)
	}

	cert, err := parseFirstCert(certPEM)
	if err != nil {
		return fmt.Errorf("tlsutil: parse cert %q: %w", certPath, err)
	}
	ca, err := parseFirstCert(caPEM)
	if err != nil {
		return fmt.Errorf("tlsutil: parse ca %q: %w", caPath, err)
	}

	roots := x509.NewCertPool()
	roots.AddCert(ca)

	usage := x509.ExtKeyUsageClientAuth
	if role == RoleServer {
		usage = x509.ExtKeyUsageServerAuth
	}

	opts := x509.VerifyOptions{
		Roots:     roots,
		KeyUsages: []x509.ExtKeyUsage{usage},
		// Use cert's NotBefore as the "current" time so an operator running
		// the check on a machine with skewed wall-clock still gets a signal
		// when the chain itself is structurally broken. Expiry is reported
		// separately in the helpful-error path below.
		CurrentTime: cert.NotBefore.Add(time.Second),
	}
	if _, verr := cert.Verify(opts); verr != nil {
		return &ChainError{
			CertPath:      certPath,
			CaPath:        caPath,
			CertSubject:   cert.Subject.String(),
			CertIssuer:    cert.Issuer.String(),
			CertNotBefore: cert.NotBefore,
			CertNotAfter:  cert.NotAfter,
			CaSubject:     ca.Subject.String(),
			CaNotBefore:   ca.NotBefore,
			CaNotAfter:    ca.NotAfter,
			Underlying:    verr,
		}
	}

	// Expiry warning is separate from chain integrity — expired but
	// chained-correctly is a different class of problem.
	now := time.Now().UTC()
	if now.After(cert.NotAfter) {
		return &ChainError{
			CertPath:      certPath,
			CaPath:        caPath,
			CertSubject:   cert.Subject.String(),
			CertIssuer:    cert.Issuer.String(),
			CertNotBefore: cert.NotBefore,
			CertNotAfter:  cert.NotAfter,
			CaSubject:     ca.Subject.String(),
			CaNotBefore:   ca.NotBefore,
			CaNotAfter:    ca.NotAfter,
			Underlying:    fmt.Errorf("cert expired %s ago", now.Sub(cert.NotAfter).Round(time.Minute)),
		}
	}

	return nil
}

// ChainError is returned by VerifyChain when the cert/CA pair does not
// form a valid client chain. The Error() string is designed for direct
// copy into an operator's terminal — it names both files, both DNs,
// both validity windows, and the remediation command.
type ChainError struct {
	CertPath      string
	CaPath        string
	CertSubject   string
	CertIssuer    string
	CertNotBefore time.Time
	CertNotAfter  time.Time
	CaSubject     string
	CaNotBefore   time.Time
	CaNotAfter    time.Time
	Underlying    error
}

// Error formats the chain failure with all context an operator needs to
// diagnose without running openssl. Example output:
//
//	tls chain invalid: /etc/cordum/tls/client/tls.crt does not chain to /etc/cordum/tls/ca/ca.crt
//	  cert:  subject="CN=cordum-client"  issuer="CN=Cordum Dev CA, O=Cordum Dev CA"
//	         valid 2026-02-15..2027-02-15
//	  ca:    subject="CN=Cordum Dev CA"  valid 2026-03-23..2036-03-20
//	  cause: x509: certificate signed by unknown authority
//	  fix:   cordumctl generate-certs --force --days 365
//	         (regenerates CA + server + client atomically)
func (e *ChainError) Error() string {
	return fmt.Sprintf(
		"tls chain invalid: %s does not chain to %s\n"+
			"  cert:  subject=%q  issuer=%q\n"+
			"         valid %s..%s\n"+
			"  ca:    subject=%q\n"+
			"         valid %s..%s\n"+
			"  cause: %v\n"+
			"  fix:   cordumctl generate-certs --force --days 365\n"+
			"         (regenerates CA + server + client atomically)",
		e.CertPath, e.CaPath,
		e.CertSubject, e.CertIssuer,
		e.CertNotBefore.Format("2006-01-02"), e.CertNotAfter.Format("2006-01-02"),
		e.CaSubject,
		e.CaNotBefore.Format("2006-01-02"), e.CaNotAfter.Format("2006-01-02"),
		e.Underlying,
	)
}

// Unwrap exposes the underlying x509 verify error so errors.Is/errors.As
// callers can distinguish chain failures from expiry failures if they care.
func (e *ChainError) Unwrap() error { return e.Underlying }

// parseFirstCert decodes the first CERTIFICATE block from PEM input and
// parses it as x509. Errors are intentionally terse — they're wrapped by
// VerifyChain with the source path for operator clarity.
func parseFirstCert(pemBytes []byte) (*x509.Certificate, error) {
	for {
		block, rest := pem.Decode(pemBytes)
		if block == nil {
			return nil, errors.New("no CERTIFICATE block found in PEM input")
		}
		if block.Type == "CERTIFICATE" {
			return x509.ParseCertificate(block.Bytes)
		}
		pemBytes = rest
	}
}
