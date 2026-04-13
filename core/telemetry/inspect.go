package telemetry

import "context"

// InspectPayload returns the most recently collected telemetry payload for
// transparency and operator review.
func InspectPayload(ctx context.Context, store *Store) (*TelemetryPayload, error) {
	if store == nil {
		return nil, nil
	}
	return store.InspectPayload(ctx)
}

// ExportPayload returns the serialized form of the last payload so callers can
// download or archive it without re-encoding.
func ExportPayload(ctx context.Context, store *Store) ([]byte, error) {
	if store == nil {
		return nil, nil
	}
	return store.ExportPayload(ctx)
}
