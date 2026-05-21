package network

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"time"
)

// SyslogIngestor binds an operator-configured UDP syslog endpoint
// and parses RFC 5424 / freeform messages. Strictly READ-only — the
// connection is created via net.ListenUDP (a server-side bind) and
// the conn handle is never used to send bytes back. This satisfies
// the NG7 contract that Cordum NEVER injects traffic.
type SyslogIngestor struct {
	network string
	addr    string
}

// syslogReadBuf bounds the per-datagram buffer. RFC 5424 caps at 64
// KiB; 8 KiB easily covers production payloads while keeping the
// per-receive allocation small.
const syslogReadBuf = 8 * 1024

// syslogPollInterval is how often Stream checks ctx.Done while
// waiting for the next datagram. Short enough that a canceled ctx is
// observed within ~200ms, long enough that an idle ingestor does not
// burn CPU.
const syslogPollInterval = 200 * time.Millisecond

// NewSyslogIngestor parses the operator-supplied endpoint URL. The
// scheme MUST be udp (TCP syslog is intentionally not supported here
// — operators forward UDP syslog from their existing fleet and
// Cordum only reads what arrives).
func NewSyslogIngestor(endpoint string) (*SyslogIngestor, error) {
	if endpoint == "" {
		return nil, errors.New("network: syslog endpoint is required")
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("network: parse syslog endpoint %q: %w", endpoint, err)
	}
	if u.Scheme != "udp" {
		return nil, fmt.Errorf("network: syslog scheme must be udp, got %q", u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("network: syslog endpoint missing host:port (%q)", endpoint)
	}
	return &SyslogIngestor{network: u.Scheme, addr: u.Host}, nil
}

// SourceLabel satisfies LogIngestor.
func (i *SyslogIngestor) SourceLabel() string { return "syslog" }

// Stream satisfies LogIngestor. Binds the configured UDP port, reads
// datagrams, parses each into a LogRecord, and forwards them to out.
// Per-datagram parse failures are silently dropped (observe-mode
// contract: don't kill the loop on a malformed line).
func (i *SyslogIngestor) Stream(ctx context.Context, out chan<- LogRecord) error {
	addr, err := net.ResolveUDPAddr(i.network, i.addr)
	if err != nil {
		return fmt.Errorf("network: resolve %q: %w", i.addr, err)
	}
	conn, err := net.ListenUDP(i.network, addr)
	if err != nil {
		return fmt.Errorf("network: listen %q: %w", i.addr, err)
	}
	defer func() { _ = conn.Close() }()

	buf := make([]byte, syslogReadBuf)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := conn.SetReadDeadline(time.Now().Add(syslogPollInterval)); err != nil {
			return fmt.Errorf("network: set deadline: %w", err)
		}
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			var nerr net.Error
			if errors.As(err, &nerr) && nerr.Timeout() {
				continue
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("network: read syslog: %w", err)
		}
		rec, ok := parseRecord(string(buf[:n]))
		if !ok {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case out <- rec:
		}
	}
}
