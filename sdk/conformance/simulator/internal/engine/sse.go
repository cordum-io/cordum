package engine

import (
	"fmt"
	"net/http"
)

// StreamEvent is one frame written to a Server-Sent Events response.
type StreamEvent struct {
	Event string
	Data  string
	ID    string
}

// WriteSSEFrames writes a fixed sequence of SSE frames to w and
// returns after the last frame is flushed. The simulator emits a
// deterministic 3-event sequence for workflow-run streams so the
// stream-fixture can assert exact event names + data payloads.
func WriteSSEFrames(w http.ResponseWriter, frames []StreamEvent) error {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("response writer does not implement http.Flusher")
	}
	for _, f := range frames {
		if f.ID != "" {
			if _, err := fmt.Fprintf(w, "id: %s\n", f.ID); err != nil {
				return err
			}
		}
		if f.Event != "" {
			if _, err := fmt.Fprintf(w, "event: %s\n", f.Event); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", f.Data); err != nil {
			return err
		}
		flusher.Flush()
	}
	return nil
}
