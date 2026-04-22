package handlers

import (
	"net/http"
	"strings"

	"github.com/cordum/cordum-sdk-conformance-simulator/internal/engine"
)

// Stream registers the SSE surfaces.
//
//	ANY /api/v1/stream          generic event stream (3 frames + close)
//	ANY /api/v1/jobs/{id}/stream job-scoped stream (alias)
func Stream(mux *http.ServeMux, eng *engine.Engine) {
	mux.HandleFunc("/api/v1/stream", func(w http.ResponseWriter, r *http.Request) {
		ac := eng.AuthFromRequest(r)
		if !ac.Authenticated() {
			engine.WriteError(w, http.StatusUnauthorized, "unauthorized", "authentication required", nil)
			return
		}
		writeStreamFrames(w, eng)
	})
}

// StreamJob registers the job-scoped streaming surface. Distinct
// handler so the simulator can ignore the path param and emit the
// same deterministic frame sequence the workflow stream fixture
// depends on.
func StreamJob(mux *http.ServeMux, eng *engine.Engine) {
	mux.HandleFunc("/api/v1/jobs/stream", func(w http.ResponseWriter, r *http.Request) {
		ac := eng.AuthFromRequest(r)
		if !ac.Authenticated() {
			engine.WriteError(w, http.StatusUnauthorized, "unauthorized", "authentication required", nil)
			return
		}
		writeStreamFrames(w, eng)
	})
}

func writeStreamFrames(w http.ResponseWriter, eng *engine.Engine) {
	// Three deterministic frames; the run_and_stream_events fixture
	// asserts exact event names + data payloads.
	runID := eng.NextID("run")
	frames := []engine.StreamEvent{
		{ID: "1", Event: "run.started", Data: `{"id":"` + runID + `"}`},
		{ID: "2", Event: "step.started", Data: `{"name":"echo"}`},
		{ID: "3", Event: "run.completed", Data: `{"status":"succeeded"}`},
	}
	_ = engine.WriteSSEFrames(w, frames)
}

// TrimPath is a tiny helper to keep handler code concise when stripping
// a known prefix to recover a path param.
func TrimPath(path, prefix string) string {
	return strings.TrimPrefix(path, prefix)
}
