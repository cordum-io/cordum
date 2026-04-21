package tlsutil

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics exported so Prometheus / Grafana can alert on cert expiry and
// chain-drift WITHOUT the service having to auto-rotate behind the
// operator's back. Prod pattern:
//
//   - cordum_cert_expires_seconds{component,role,path} — seconds until NotAfter.
//     Alert rule: fire when value < 14d (1209600s), page when <7d (604800s).
//   - cordum_cert_chain_valid{component,role,path}     — 1 if cert chains to
//     configured CA at load time, 0 otherwise. Alert rule: fire immediately on 0.
//
// Emit once per cert at service startup (in the TLS loader). Values are
// static until the service restarts, which is the correct model: certs don't
// rotate at runtime, so the gauge doesn't tick. On restart the service
// re-registers and re-emits. Grafana uses the latest sample.
var (
	certExpirySeconds = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "cordum_cert_expires_seconds",
		Help: "Seconds until the certificate's NotAfter. Negative means expired. Alert at <14d (1209600s).",
	}, []string{"component", "role", "path"})

	certChainValid = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "cordum_cert_chain_valid",
		Help: "1 if the certificate chains to the configured CA at load time, 0 otherwise. 0 means the stack will fail TLS handshakes — alert immediately.",
	}, []string{"component", "role", "path"})

	emitOnce sync.Map // key = component|role|path → struct{} — idempotent on reload.
)

// EmitCertMetrics publishes expiry + chain-validity gauges for a loaded cert.
// Safe to call multiple times for the same (component, role, path) — subsequent
// calls update the gauge value but don't double-register.
//
// Callers pass a valid *tls.Certificate (what LoadX509KeyPair returned) plus
// the chain-verify verdict. The helper accepts both pieces so a chain-invalid
// cert still emits an expiry signal — ops want to know BOTH failure modes,
// not just the first one.
func EmitCertMetrics(component, role, path string, notAfter time.Time, chainValid bool) {
	remaining := time.Until(notAfter).Seconds()
	certExpirySeconds.WithLabelValues(component, role, path).Set(remaining)
	if chainValid {
		certChainValid.WithLabelValues(component, role, path).Set(1)
	} else {
		certChainValid.WithLabelValues(component, role, path).Set(0)
	}
	key := component + "|" + role + "|" + path
	emitOnce.Store(key, struct{}{})
}

// SnapshotCerts returns a read-only snapshot of every cert reported via
// EmitCertMetrics since this process started. The gateway's
// /api/v1/system/certs handler consumes this to feed the dashboard cert
// health widget without scraping Prometheus from the React side.
func SnapshotCerts() []CertStatus {
	var out []CertStatus
	emitOnce.Range(func(k, _ any) bool {
		key, ok := k.(string)
		if !ok {
			return true
		}
		// Parse back component|role|path. Paths don't contain '|' on any
		// supported OS, but be defensive against future code.
		component, role, path := splitThree(key, '|')
		out = append(out, CertStatus{
			Component: component,
			Role:      role,
			Path:      path,
		})
		return true
	})
	return out
}

// CertStatus is the wire shape returned by SnapshotCerts. The HTTP handler
// enriches it with the current gauge values so the dashboard renders both
// chain + expiry in one call.
type CertStatus struct {
	Component string `json:"component"`
	Role      string `json:"role"`
	Path      string `json:"path"`
}

func splitThree(s string, sep rune) (a, b, c string) {
	first := -1
	second := -1
	for i, r := range s {
		if r != sep {
			continue
		}
		if first < 0 {
			first = i
			continue
		}
		second = i
		break
	}
	if first < 0 {
		return s, "", ""
	}
	if second < 0 {
		return s[:first], s[first+1:], ""
	}
	return s[:first], s[first+1 : second], s[second+1:]
}
