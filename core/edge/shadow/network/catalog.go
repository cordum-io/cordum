package network

// enforceCatalog strips fields outside the §9.1 lawful-metadata
// catalog. Even though ingestors should never populate the forbidden
// fields (RawURL / BearerToken / RemoteIP), defense in depth: the
// returned record cannot leak those values into any later code path
// because the values are zeroed at this single chokepoint.
//
// Allowed fields (§9.1): Timestamp, Hostname, WorkloadID, OIDCSub,
// EndpointHash, Count. Everything else is forbidden by the catalog.
func enforceCatalog(rec LogRecord) LogRecord {
	return LogRecord{
		Timestamp:    rec.Timestamp,
		Hostname:     rec.Hostname,
		WorkloadID:   rec.WorkloadID,
		OIDCSub:      rec.OIDCSub,
		EndpointHash: rec.EndpointHash,
		Count:        rec.Count,
		// RawURL / BearerToken / RemoteIP intentionally NOT copied.
	}
}

// countBucket rounds a raw observation count up to the nearest power-
// of-ten bucket (1, 10, 100, 1000, 10000, 10000+). Buckets prevent
// the persisted finding from leaking exact request rates while still
// letting operators distinguish "saw it once" from "saw it 10k
// times". Mirrors design doc §9.1 count-bucket spec.
func countBucket(count int) int {
	switch {
	case count <= 1:
		return 1
	case count <= 10:
		return 10
	case count <= 100:
		return 100
	case count <= 1000:
		return 1000
	case count <= 10000:
		return 10000
	default:
		return 100000
	}
}
