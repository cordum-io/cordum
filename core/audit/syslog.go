package audit

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

// Syslog facility codes (RFC 5424 §6.2.1).
const (
	FacilityLocal0 = 16 // local0
	FacilityLocal7 = 23 // local7
)

const syslogWriteTimeout = 5 * time.Second

// Private Enterprise Number for structured data.
// 49462 is a placeholder; replace with IANA-assigned PEN for production.
const cordumPEN = "49462"

// SyslogExporter sends SIEM events as RFC 5424 messages over TCP or UDP.
type SyslogExporter struct {
	mu       sync.Mutex
	conn     net.Conn
	network  string
	address  string
	facility int
	appName  string
	hostname string
}

// SyslogOption configures a SyslogExporter.
type SyslogOption func(*SyslogExporter)

// WithSyslogFacility sets the syslog facility code (default: local0/16).
func WithSyslogFacility(f int) SyslogOption {
	return func(s *SyslogExporter) { s.facility = f }
}

// WithSyslogAppName sets the APP-NAME field (default: "cordum").
func WithSyslogAppName(name string) SyslogOption {
	return func(s *SyslogExporter) { s.appName = name }
}

// NewSyslogExporter creates a syslog exporter that sends RFC 5424 messages.
// Network is "tcp" or "udp"; address is "host:port".
func NewSyslogExporter(network, address string, opts ...SyslogOption) (*SyslogExporter, error) {
	hostname, _ := os.Hostname()
	s := &SyslogExporter{
		network:  network,
		address:  address,
		facility: FacilityLocal0,
		appName:  "cordum",
		hostname: hostname,
	}
	for _, o := range opts {
		o(s)
	}
	if err := s.connect(); err != nil {
		return nil, fmt.Errorf("audit syslog dial %s/%s: %w", network, address, err)
	}
	return s, nil
}

// Export sends each event as an RFC 5424 syslog message.
func (s *SyslogExporter) Export(_ context.Context, events []SIEMEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, ev := range events {
		msg := s.formatRFC5424(ev)
		_ = s.conn.SetWriteDeadline(time.Now().Add(syslogWriteTimeout))
		if _, err := fmt.Fprint(s.conn, msg); err != nil {
			// Reconnect once and retry.
			if cerr := s.connect(); cerr != nil {
				return fmt.Errorf("audit syslog reconnect: %w", cerr)
			}
			_ = s.conn.SetWriteDeadline(time.Now().Add(syslogWriteTimeout))
			if _, err = fmt.Fprint(s.conn, msg); err != nil {
				return fmt.Errorf("audit syslog write: %w", err)
			}
		}
	}
	return nil
}

// Close closes the underlying network connection.
func (s *SyslogExporter) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}

func (s *SyslogExporter) connect() error {
	if s.conn != nil {
		if err := s.conn.Close(); err != nil {
			return err
		}
		s.conn = nil
	}
	conn, err := net.DialTimeout(s.network, s.address, 5*time.Second)
	if err != nil {
		return err
	}
	s.conn = conn
	return nil
}

// severityCode maps SIEM severity to RFC 5424 numeric severity.
func severityCode(sev string) int {
	switch strings.ToUpper(sev) {
	case SeverityCritical:
		return 2 // crit
	case SeverityHigh:
		return 3 // err
	case SeverityMedium:
		return 4 // warning
	case SeverityLow:
		return 5 // notice
	case SeverityInfo:
		return 6 // info
	default:
		return 6
	}
}

// formatRFC5424 builds an RFC 5424 message with structured data.
// Format: <PRI>1 TIMESTAMP HOSTNAME APP-NAME PID MSGID [SD] MSG\n
func (s *SyslogExporter) formatRFC5424(ev SIEMEvent) string {
	pri := s.facility*8 + severityCode(ev.Severity)
	ts := ev.Timestamp.UTC().Format(time.RFC3339Nano)
	pid := os.Getpid()

	// Structured data block: [cordum@PEN key="val" ...]
	sd := fmt.Sprintf("[cordum@%s event_type=\"%s\" action=\"%s\" decision=\"%s\" tenant=\"%s\" identity=\"%s\"]",
		cordumPEN,
		escapeSD(ev.EventType),
		escapeSD(ev.Action),
		escapeSD(ev.Decision),
		escapeSD(ev.TenantID),
		escapeSD(ev.Identity),
	)

	// Human-readable message
	msg := ev.Reason
	if msg == "" {
		msg = ev.Action
	}

	return fmt.Sprintf("<%d>1 %s %s %s %d - %s %s\n",
		pri, ts, s.hostname, s.appName, pid, sd, msg)
}

// escapeSD escapes characters per RFC 5424 §6.3.3.
func escapeSD(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, `]`, `\]`)
	return s
}
