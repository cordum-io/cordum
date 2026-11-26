package mcp

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

// TestDirectBridge_ReadHooks pins DirectServiceBridge's GetJob and GetStatus
// hooks. Without a hook each still answers 501 read_only_unsupported. With
// one, GetJob trims the ID, refuses a blank one before the hook runs, and
// both return the hook's item or error unchanged.
func TestDirectBridge_ReadHooks(t *testing.T) {
	ctx := context.Background()
	requireUnsupported := func(t *testing.T, name string, err error) {
		t.Helper()
		var berr *BridgeError
		if !errors.As(err, &berr) || berr.StatusCode != http.StatusNotImplemented || berr.Code != "read_only_unsupported" {
			t.Fatalf("%s error = %v, want 501 read_only_unsupported", name, err)
		}
	}

	t.Run("no hooks answer 501", func(t *testing.T) {
		for _, b := range []*DirectServiceBridge{nil, NewDirectServiceBridge(DirectServiceBridgeConfig{})} {
			item, err := b.GetJob(ctx, "job-1")
			if item != nil {
				t.Fatalf("GetJob item = %+v, want nil", item)
			}
			requireUnsupported(t, "GetJob", err)
			item, err = b.GetStatus(ctx)
			if item != nil {
				t.Fatalf("GetStatus item = %+v, want nil", item)
			}
			requireUnsupported(t, "GetStatus", err)
		}
	})

	var gotIDs []string
	hookErr := NewBridgeError(http.StatusForbidden, "forbidden", "tenant access denied", nil)
	b := NewDirectServiceBridge(DirectServiceBridgeConfig{
		GetJobFunc: func(_ context.Context, jobID string) (*ResourceItem, error) {
			gotIDs = append(gotIDs, jobID)
			if jobID == "job-denied" {
				return nil, hookErr
			}
			return &ResourceItem{ID: jobID, Kind: "job", Data: map[string]any{"id": jobID}}, nil
		},
		GetStatusFunc: func(context.Context) (*ResourceItem, error) {
			return &ResourceItem{Kind: "status", Data: map[string]any{"uptime_seconds": 7}}, nil
		},
	})

	t.Run("GetJob trims and delegates", func(t *testing.T) {
		item, err := b.GetJob(ctx, "  job-1 ")
		if err != nil || item == nil || item.ID != "job-1" || item.Kind != "job" || item.Data["id"] != "job-1" {
			t.Fatalf("GetJob = %+v, %v; want job-1", item, err)
		}
	})
	t.Run("GetJob refuses a blank id before the hook", func(t *testing.T) {
		before := len(gotIDs)
		for _, id := range []string{"", "   "} {
			item, err := b.GetJob(ctx, id)
			if item != nil || !errors.Is(err, ErrInvalidParams) {
				t.Fatalf("GetJob(%q) = %+v, %v; want ErrInvalidParams", id, item, err)
			}
		}
		if len(gotIDs) != before {
			t.Fatalf("hook ran for a blank id: %v", gotIDs[before:])
		}
	})
	t.Run("GetJob returns the hook error unchanged", func(t *testing.T) {
		item, err := b.GetJob(ctx, "job-denied")
		if item != nil || err != hookErr {
			t.Fatalf("GetJob(job-denied) = %+v, %v; want nil and the hook's error", item, err)
		}
	})
	t.Run("GetStatus delegates", func(t *testing.T) {
		item, err := b.GetStatus(ctx)
		if err != nil || item == nil || item.Kind != "status" || item.Data["uptime_seconds"] != 7 {
			t.Fatalf("GetStatus = %+v, %v; want the hook's status item", item, err)
		}
	})
	if want := []string{"job-1", "job-denied"}; len(gotIDs) != len(want) || gotIDs[0] != want[0] || gotIDs[1] != want[1] {
		t.Fatalf("hook ids = %v, want %v", gotIDs, want)
	}
}
