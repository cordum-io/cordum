package network

import (
	"strconv"
	"strings"
	"time"
)

// parseRecord parses a single log line into a LogRecord. The line
// format is space-separated, with an optional RFC 5424 syslog header
// followed by space-separated `key=value` pairs. Recognised keys are
// the §9.1 catalog fields (hostname / workload / oidc_sub / endpoint
// / count) plus the boundary-only forbidden fields (raw_url / bearer
// / remote_ip) which enforceCatalog drops before persistence.
//
// Unrecognised keys are silently dropped (defense in depth — a
// malicious log writer cannot inject arbitrary field names into the
// persisted finding). An ISO-8601 timestamp anywhere in the line
// populates rec.Timestamp; explicit timestamp= key wins if both
// present.
//
// Returns (zero, false) when no key=value pair is present on the
// line; the caller (ingestor) treats that as "no record to emit" and
// continues with the next line. Empty / whitespace-only lines yield
// the same (zero, false).
func parseRecord(line string) (LogRecord, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return LogRecord{}, false
	}
	fields := strings.Fields(line)
	var rec LogRecord
	sawKV := false
	for _, f := range fields {
		eq := strings.IndexByte(f, '=')
		if eq <= 0 {
			if rec.Timestamp.IsZero() {
				if t, err := time.Parse(time.RFC3339, f); err == nil {
					rec.Timestamp = t
				}
			}
			continue
		}
		key := f[:eq]
		val := f[eq+1:]
		switch key {
		case "hostname":
			rec.Hostname = val
			sawKV = true
		case "workload":
			rec.WorkloadID = val
			sawKV = true
		case "oidc_sub":
			rec.OIDCSub = val
			sawKV = true
		case "endpoint":
			rec.EndpointHash = val
			sawKV = true
		case "count":
			if n, err := strconv.Atoi(val); err == nil {
				rec.Count = n
			}
			sawKV = true
		case "timestamp":
			if t, err := time.Parse(time.RFC3339, val); err == nil {
				rec.Timestamp = t
			}
			sawKV = true
		case "raw_url":
			rec.RawURL = val
			sawKV = true
		case "bearer":
			rec.BearerToken = val
			sawKV = true
		case "remote_ip":
			rec.RemoteIP = val
			sawKV = true
		default:
			// Unknown key — drop silently. No leakage into LogRecord.
		}
	}
	if !sawKV {
		return LogRecord{}, false
	}
	return rec, true
}
