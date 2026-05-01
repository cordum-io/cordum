package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/licensing"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	wf "github.com/cordum/cordum/core/workflow"
	"github.com/gorilla/websocket"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

var wsPacketsDroppedTotal = promauto.NewCounter(prometheus.CounterOpts{
	Name: "cordum_gateway_ws_packets_dropped_total",
	Help: "Total WebSocket bus packets dropped due to marshal failure",
})

var wsEventsDropped = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "cordum_gateway_ws_events_dropped_total",
	Help: "Total WebSocket events dropped before broadcast",
}, []string{"reason"})

var wsSlowClientEvictions = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "cordum_gateway_ws_slow_client_evictions_total",
	Help: "Total WebSocket clients evicted because their send buffer was full",
}, []string{"reason"})

var wsClientsActive = promauto.NewGauge(prometheus.GaugeOpts{
	Name: "cordum_gateway_ws_clients_active",
	Help: "Current active WebSocket connections",
})

var wsConnectionDuration = promauto.NewHistogram(prometheus.HistogramOpts{
	Name:    "cordum_gateway_ws_connection_duration_seconds",
	Help:    "WebSocket connection lifetime in seconds",
	Buckets: []float64{1, 10, 60, 300, 900, 1800, 3600, 7200, 14400},
})

var wsPingsSent = promauto.NewCounter(prometheus.CounterOpts{
	Name: "cordum_gateway_ws_pings_sent_total",
	Help: "Total WebSocket ping frames sent",
})

var wsPongsReceived = promauto.NewCounter(prometheus.CounterOpts{
	Name: "cordum_gateway_ws_pongs_received_total",
	Help: "Total WebSocket pong frames received",
})

var wsPongTimeouts = promauto.NewCounter(prometheus.CounterOpts{
	Name: "cordum_gateway_ws_pong_timeouts_total",
	Help: "Total WebSocket connections closed after missing pong deadlines",
})

var wsRevalidation = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "cordum_gateway_ws_revalidation_total",
	Help: "Total WebSocket credential revalidation outcomes",
}, []string{"outcome"})

var wsReconnections = promauto.NewCounter(prometheus.CounterOpts{
	Name: "cordum_gateway_ws_reconnections_total",
	Help: "Total WebSocket client reconnections within the recent reconnect window",
})

// wsQuarantineRedactionDrops counts broadcasts the gateway DROPPED because
// the quarantine-redaction filter could not produce a sanitised clone of a
// DENIED JobResult. Dropping is the fail-closed posture — the original
// packet carries ResultPtr + ArtifactPtrs that may contain PII, secrets, or
// model outputs; those must not leak to WebSocket subscribers (including
// cross-tenant ones with allowCrossTenant granted). See task-1d4e6b4c.
var wsQuarantineRedactionDrops = promauto.NewCounter(prometheus.CounterOpts{
	Name: "cordum_gateway_ws_quarantine_redaction_drops_total",
	Help: "Total WebSocket broadcasts dropped because the quarantine-redaction filter could not produce a sanitised clone",
})

const (
	defaultWSClientBufSize = 256
	minWSClientBufSize     = 1
	maxWSClientBufSize     = 10000
	wsReconnectWindow      = 60 * time.Second
	disconnectClientClose  = "client_close"
	disconnectPingTimeout  = "ping_timeout"
	disconnectRevalidation = "revalidation_revoked"
	disconnectContextDone  = "context_cancelled"
	disconnectWriteError   = "write_error"
	disconnectServerDown   = "server_shutdown"
	disconnectBufferFull   = "buffer_full"
)

// wsClientBufferSize reads CORDUM_WS_CLIENT_BUFFER_SIZE and clamps to [1, 10000].
func wsClientBufferSize() int {
	raw := strings.TrimSpace(os.Getenv("CORDUM_WS_CLIENT_BUFFER_SIZE"))
	if raw == "" {
		return defaultWSClientBufSize
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < minWSClientBufSize {
		slog.Warn("invalid CORDUM_WS_CLIENT_BUFFER_SIZE, using default",
			"value", raw, "default", defaultWSClientBufSize)
		return defaultWSClientBufSize
	}
	if v > maxWSClientBufSize {
		slog.Warn("CORDUM_WS_CLIENT_BUFFER_SIZE exceeds max, clamping",
			"requested", v, "max", maxWSClientBufSize)
		return maxWSClientBufSize
	}
	return v
}

// stopBusTaps shuts down the broadcast goroutine by closing eventsCh.
// It is safe to call multiple times.
func (s *server) stopBusTaps() {
	s.stopBusTapsOnce.Do(func() {
		s.eventsStopped.Store(true)
		if s.eventsCh != nil {
			close(s.eventsCh)
		}
	})
}

// wsRevalidateInterval controls how often long-lived WebSocket connections
// re-check the caller's API key. If the key has been revoked or rotated the
// connection is closed within this window.
var (
	wsRevalidateInterval   = 120 * time.Second
	wsRevalidateRetryDelay = 500 * time.Millisecond
	wsTimingMu             sync.RWMutex
)

func currentWSTimings() (pingInterval, pongTimeout, revalidateInterval, revalidateRetryDelay time.Duration) {
	wsTimingMu.RLock()
	defer wsTimingMu.RUnlock()
	return wsPingInterval, wsPongTimeout, wsRevalidateInterval, wsRevalidateRetryDelay
}

type wsClient struct {
	ch               chan wsEvent
	tenant           string
	allowCrossTenant bool
	jobID            string
	apiKey           string // stored for periodic revalidation, never logged
	closeOnce        sync.Once
}

// closeChannel closes the client's event channel exactly once.
// Safe to call from both the broadcast loop (eviction) and handler defer.
func (c *wsClient) closeChannel() {
	c.closeOnce.Do(func() { close(c.ch) })
}

type wsEvent struct {
	data   []byte
	tenant string
	jobID  string
}

type wsDisconnectState struct {
	mu     sync.Mutex
	reason string
	err    error
}

func (s *wsDisconnectState) Set(reason string, err error) {
	if s == nil || strings.TrimSpace(reason) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reason = reason
	if err != nil {
		s.err = err
	}
}

func (s *wsDisconnectState) SetIfOneOf(reason string, err error, replaceable ...string) {
	if s == nil || strings.TrimSpace(reason) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reason != "" {
		allowed := false
		for _, candidate := range replaceable {
			if s.reason == candidate {
				allowed = true
				break
			}
		}
		if !allowed {
			return
		}
	}
	s.reason = reason
	if err != nil {
		s.err = err
	}
}

func (s *wsDisconnectState) Snapshot(defaultReason string) (string, error) {
	if s == nil {
		return defaultReason, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(s.reason) == "" {
		return defaultReason, s.err
	}
	return s.reason, s.err
}

func newWSConnectionID() string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		fallback := fmt.Sprintf("%016x", time.Now().UnixNano())
		slog.Warn("ws connection id generation fell back to timestamp", "error", err)
		return fallback
	}
	return hex.EncodeToString(raw[:])
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return isAllowedOrigin(r) },
}

// negotiateSubprotocol builds a response header that echoes back only the
// "cordum-api-key" subprotocol identifier — never the credential itself.
// Previous versions echoed the full dot-format token (cordum-api-key.<base64>),
// which leaked the API key in response headers visible to proxies and DevTools.
func negotiateSubprotocol(r *http.Request) http.Header {
	for _, p := range websocket.Subprotocols(r) {
		if strings.EqualFold(p, wsAuthSubprotocol) {
			// Comma-separated format: ["cordum-api-key", "<base64>"]
			// Echo back the bare identifier only.
			return http.Header{"Sec-Websocket-Protocol": {wsAuthSubprotocol}}
		}
		if strings.HasPrefix(strings.ToLower(p), strings.ToLower(wsAuthSubprotocol)+".") {
			// Legacy dot format: "cordum-api-key.<base64>"
			// Still accept for backward compat, but only echo the identifier.
			return http.Header{"Sec-Websocket-Protocol": {wsAuthSubprotocol}}
		}
	}
	return nil
}

// startWorkerExpiry launches a background goroutine that evicts stale entries
// from the workerSeen and workers maps. This prevents unbounded growth when
// workers disconnect without sending a final heartbeat.
func (s *server) startWorkerExpiry() {
	s.workerExpireStop = make(chan struct{})
	go func() {
		ticker := time.NewTicker(workerHeartbeatTTL / 2)
		defer ticker.Stop()
		for {
			select {
			case <-s.workerExpireStop:
				return
			case <-ticker.C:
				now := time.Now().UTC()
				cutoff := now.Add(-workerHeartbeatTTL)
				s.workerMu.Lock()
				for id, seen := range s.workerSeen {
					if seen.Before(cutoff) {
						delete(s.workerSeen, id)
						delete(s.workers, id)
					}
				}
				s.workerMu.Unlock()
			}
		}
	}()
}

// stopWorkerExpiry signals the expiry goroutine to stop. Safe to call multiple times.
func (s *server) stopWorkerExpiry() {
	s.workerExpireOnce.Do(func() {
		if s.workerExpireStop != nil {
			close(s.workerExpireStop)
		}
	})
}

// startBusTaps subscribes to heartbeats and system events once for the lifetime of the gateway.
func (s *server) startBusTaps() error {
	// Heartbeats -> worker registry snapshot
	if err := s.bus.Subscribe(capsdk.SubjectHeartbeat, "", func(p *pb.BusPacket) error {
		if hb := p.GetHeartbeat(); hb != nil {
			s.workerMu.Lock()
			s.workers[hb.WorkerId] = hb
			if s.workerSeen == nil {
				s.workerSeen = make(map[string]time.Time)
			}
			s.workerSeen[hb.WorkerId] = time.Now().UTC()
			s.workerMu.Unlock()
			// Also stream heartbeats to WS listeners (best effort).
			s.enqueueBusPacket(p)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("subscribe %s: %w", capsdk.SubjectHeartbeat, err)
	}

	// DLQ tap to persist entries. Uses queue group so only one gateway replica
	// writes to Redis per DLQ event (N replicas → 1 write instead of N).
	// WS forwarding is handled separately by the sys.job.> broadcast subscription.
	if s.dlqStore != nil {
		if err := s.bus.Subscribe(capsdk.SubjectDLQ, "cordum-gateway", func(p *pb.BusPacket) error {
			if jr := p.GetJobResult(); jr != nil {
				jobID := strings.TrimSpace(jr.JobId)
				topic := ""
				lastState := ""
				attempts := 0
				dlqCtx, dlqCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer dlqCancel()
				if s.jobStore != nil && jobID != "" {
					if t, err := s.jobStore.GetTopic(dlqCtx, jobID); err == nil {
						topic = t
					}
					if st, err := s.jobStore.GetState(dlqCtx, jobID); err == nil {
						lastState = string(st)
					}
					if a, err := s.jobStore.GetAttempts(dlqCtx, jobID); err == nil {
						attempts = a
					}
				}
				if err := s.dlqStore.Add(dlqCtx, store.DLQEntry{
					JobID:      jobID,
					Topic:      topic,
					Status:     jr.Status.String(),
					Reason:     jr.ErrorMessage,
					ReasonCode: strings.TrimSpace(jr.ErrorCode),
					LastState:  lastState,
					Attempts:   attempts,
					CreatedAt:  time.Now().UTC(),
				}); err != nil {
					slog.Error("dlq add failed", "job_id", jobID, "error", err)
				}

				// Best effort: ensure a result exists for failed-to-dispatch jobs so clients can inspect `res:<job_id>`.
				if s.memStore != nil && s.jobStore != nil && jobID != "" {
					resKey := store.MakeResultKey(jobID)
					resPtr := store.PointerForKey(resKey)
					body := map[string]any{
						"job_id":       jobID,
						"status":       jr.Status.String(),
						"error":        map[string]any{"message": jr.ErrorMessage},
						"processed_by": "cordum-scheduler",
						"completed_at": time.Now().UTC().Format(time.RFC3339),
					}
					if data, err := json.Marshal(body); err == nil {
						if err := s.memStore.PutResult(dlqCtx, resKey, data); err != nil {
							slog.Error("store result failed", "job_id", jobID, "error", err)
						}
					}
					if existing, err := s.jobStore.GetResultPtr(dlqCtx, jobID); err != nil || strings.TrimSpace(existing) == "" {
						if err := s.jobStore.SetResultPtr(dlqCtx, jobID, resPtr); err != nil {
							slog.Error("set result pointer failed", "job_id", jobID, "error", err)
						}
					}
				}
			}
			return nil
		}); err != nil {
			return fmt.Errorf("subscribe %s: %w", capsdk.SubjectDLQ, err)
		}
	}

	// Event taps -> broadcast channel
	for _, subj := range []string{"sys.job.>", "sys.audit.>"} {
		subject := subj
		if err := s.bus.Subscribe(subject, "", func(p *pb.BusPacket) error {
			// Always broadcast to WS clients first — duplicate broadcasts are
			// harmless (dashboard re-renders) and ensures visibility even on retry.
			s.enqueueBusPacket(p)
			if subject == "sys.job.>" {
				// Check if gateway is shutting down before starting
				// potentially long-running workflow result processing.
				select {
				case <-s.shutdownCh:
					return nil
				default:
				}
				handlerCtx, handlerCancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer handlerCancel()
				if err := s.handleWorkflowJobResult(handlerCtx, p.GetJobResult()); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			slog.Error("bus subscribe failed", "subject", subject, "error", err)
		}
	}

	// Broadcast loop to WS clients
	go func() {
		for {
			select {
			case evt, ok := <-s.eventsCh:
				if !ok {
					return
				}
				var slowClients []*websocket.Conn
				// Write lock covers detect-slow + delete to avoid TOCTOU race.
				s.clientsMu.Lock()
				for conn, client := range s.clients {
					if client == nil {
						continue
					}
					if !client.allowCrossTenant {
						if evt.tenant == "" || evt.tenant != client.tenant {
							continue
						}
					}
					if client.jobID != "" && evt.jobID != client.jobID {
						continue
					}
					select {
					case client.ch <- evt:
					default:
						slowClients = append(slowClients, conn)
					}
				}
				for _, conn := range slowClients {
					if client := s.clients[conn]; client != nil {
						client.closeChannel()
					}
					delete(s.clients, conn)
				}
				s.clientsMu.Unlock()
				if n := len(slowClients); n > 0 {
					wsSlowClientEvictions.WithLabelValues("buffer_full").Add(float64(n))
					slog.Warn("ws: evicted slow clients", "count", n)
				}
			case <-s.shutdownCh:
				return
			}
		}
	}()

	s.startWorkerExpiry()

	return nil
}

func (s *server) enqueueBusPacket(p *pb.BusPacket) {
	if s == nil || p == nil {
		return
	}

	// Filter quarantined/denied job results: strip sensitive content before
	// broadcast. filterQuarantinedPacket returns nil on any failure branch
	// (clone failed, cloned JobResult was nil); in that case the fail-closed
	// contract says drop the broadcast — the next state-change event will
	// follow in the normal stream cadence. See task-1d4e6b4c.
	filtered := filterQuarantinedPacket(p)
	if filtered == nil {
		return
	}
	p = filtered

	data, err := marshalBusPacketForWS(p)
	if err != nil {
		wsPacketsDroppedTotal.Inc()
		slog.Error(
			"websocket bus packet dropped after all marshal attempts failed",
			"packet_type", busPacketType(p),
			"trace_id", sanitizeUTF8ForLog(strings.TrimSpace(p.GetTraceId())),
			"sender_id", sanitizeUTF8ForLog(strings.TrimSpace(p.GetSenderId())),
			"error", err,
		)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	tenant, _ := s.tenantForBusPacket(ctx, p)
	cancel()
	jobID := jobIDForBusPacket(p)
	s.enqueueWSEvent(data, tenant, jobID)
}

func marshalBusPacketForWS(p *pb.BusPacket) ([]byte, error) {
	if p == nil {
		return nil, errors.New("bus packet required")
	}

	data, err := protojson.Marshal(p)
	if err == nil {
		return data, nil
	}

	packetType := busPacketType(p)
	traceID := sanitizeUTF8ForLog(strings.TrimSpace(p.GetTraceId()))
	slog.Error(
		"protojson marshal failed for websocket bus packet",
		"packet_type", packetType,
		"trace_id", traceID,
		"error", err,
	)

	data, sanitizedErr := marshalSanitizedBusPacketForWS(p)
	if sanitizedErr == nil {
		return data, nil
	}

	data, fallbackErr := json.Marshal(p)
	if fallbackErr == nil {
		slog.Error(
			"sanitized protojson fallback failed; using stdlib JSON fallback for websocket bus packet",
			"packet_type", packetType,
			"trace_id", traceID,
			"error", sanitizedErr,
		)
		return data, nil
	}

	slog.Error(
		"failed to marshal websocket bus packet fallback; dropping packet",
		"packet_type", packetType,
		"trace_id", traceID,
		"sanitize_error", sanitizedErr,
		"error", fallbackErr,
	)
	return nil, fallbackErr
}

func marshalSanitizedBusPacketForWS(p *pb.BusPacket) ([]byte, error) {
	cloned, ok := proto.Clone(p).(*pb.BusPacket)
	if !ok || cloned == nil {
		return nil, errors.New("failed to clone bus packet for websocket fallback")
	}
	sanitizeProtoStrings(cloned.ProtoReflect())
	return protojson.Marshal(cloned)
}

func sanitizeProtoStrings(msg protoreflect.Message) {
	if !msg.IsValid() {
		return
	}

	msg.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		switch {
		case fd.IsMap():
			sanitizeProtoMapField(msg, fd, v.Map())
		case fd.IsList():
			sanitizeProtoListField(fd, v.List())
		case fd.Kind() == protoreflect.MessageKind:
			sanitizeProtoStrings(v.Message())
		case fd.Kind() == protoreflect.StringKind:
			sanitized := sanitizeUTF8ForLog(v.String())
			if sanitized != v.String() {
				msg.Set(fd, protoreflect.ValueOfString(sanitized))
			}
		}
		return true
	})
}

func sanitizeProtoListField(fd protoreflect.FieldDescriptor, list protoreflect.List) {
	switch fd.Kind() {
	case protoreflect.StringKind:
		for i := 0; i < list.Len(); i++ {
			current := list.Get(i).String()
			sanitized := sanitizeUTF8ForLog(current)
			if sanitized != current {
				list.Set(i, protoreflect.ValueOfString(sanitized))
			}
		}
	case protoreflect.MessageKind:
		for i := 0; i < list.Len(); i++ {
			sanitizeProtoStrings(list.Get(i).Message())
		}
	}
}

func sanitizeProtoMapField(msg protoreflect.Message, fd protoreflect.FieldDescriptor, m protoreflect.Map) {
	type mapEntry struct {
		key   protoreflect.MapKey
		value protoreflect.Value
	}

	entries := make([]mapEntry, 0)
	changed := false

	m.Range(func(k protoreflect.MapKey, v protoreflect.Value) bool {
		entry := mapEntry{key: k, value: v}

		if fd.MapKey().Kind() == protoreflect.StringKind {
			sanitizedKey := sanitizeUTF8ForLog(k.String())
			if sanitizedKey != k.String() {
				entry.key = protoreflect.ValueOfString(sanitizedKey).MapKey()
				changed = true
			}
		}

		switch fd.MapValue().Kind() {
		case protoreflect.StringKind:
			sanitizedValue := sanitizeUTF8ForLog(v.String())
			if sanitizedValue != v.String() {
				entry.value = protoreflect.ValueOfString(sanitizedValue)
				changed = true
			}
		case protoreflect.MessageKind:
			sanitizeProtoStrings(v.Message())
		}

		entries = append(entries, entry)
		return true
	})

	if !changed {
		return
	}

	msg.Clear(fd)
	dst := msg.Mutable(fd).Map()
	for _, entry := range entries {
		dst.Set(entry.key, entry.value)
	}
}

func sanitizeUTF8ForLog(value string) string {
	return strings.ToValidUTF8(value, "\uFFFD")
}

func busPacketType(p *pb.BusPacket) string {
	if p == nil {
		return "unknown"
	}
	switch p.GetPayload().(type) {
	case *pb.BusPacket_JobRequest:
		return "job_request"
	case *pb.BusPacket_JobResult:
		return "job_result"
	case *pb.BusPacket_Heartbeat:
		return "heartbeat"
	case *pb.BusPacket_Alert:
		return "alert"
	case *pb.BusPacket_JobProgress:
		return "job_progress"
	case *pb.BusPacket_JobCancel:
		return "job_cancel"
	default:
		return "unknown"
	}
}

// packetCloneForFilter deep-copies a BusPacket for the quarantine-redaction
// filter. Factored out as a package-level var so tests can inject a failure
// stub (return nil) that exercises the fail-closed branch without relying on
// proto-library internals. Production path: proto.Clone + type-assert. On
// any failure we return nil — the filter translates that into a dropped
// broadcast, never a leak. See task-1d4e6b4c.
var packetCloneForFilter = func(p *pb.BusPacket) *pb.BusPacket {
	clone, ok := proto.Clone(p).(*pb.BusPacket)
	if !ok || clone == nil {
		return nil
	}
	return clone
}

// wsReadDeadliner is the minimal subset of *websocket.Conn the gateway
// needs when setting a read deadline. Exposed as an interface so tests
// can inject a stub that returns a failure (e.g. io.ErrClosedPipe) and
// verify the handler closes the connection instead of limping along
// with no deadline. See task-1d4e6b4c.
type wsReadDeadliner interface {
	SetReadDeadline(t time.Time) error
}

// setReadDeadlineOrError wraps conn.SetReadDeadline so the handler can
// tell success from silent failure. SetReadDeadline in gorilla/websocket
// returns an error when the underlying TCP socket is already closed —
// discarding that error leaves the read loop with no deadline and the
// server waits indefinitely for a read that never comes. See bug #2 in
// task-1d4e6b4c.
func setReadDeadlineOrError(conn wsReadDeadliner, ttl time.Duration) error {
	return conn.SetReadDeadline(time.Now().Add(ttl))
}

// filterQuarantinedPacket strips result payloads from quarantined/denied job
// results so that sensitive content (secrets, PII, prompts, model outputs)
// is not broadcast to WebSocket clients. Status and metadata are preserved
// so the dashboard can still show the state transition.
//
// Returns nil on any failure branch — packetCloneForFilter returned nil, or
// the clone's JobResult field was nil. Callers MUST nil-check and skip the
// broadcast; the next state-change event will follow in the normal stream
// cadence. This is the fail-CLOSED contract; the pre-fix code returned the
// original unredacted packet on both failure branches, a data-leak bug
// surfaced by the 2026-04-23 WebSocket audit. See task-1d4e6b4c.
func filterQuarantinedPacket(p *pb.BusPacket) *pb.BusPacket {
	jr := p.GetJobResult()
	if jr == nil {
		return p
	}
	if jr.Status != pb.JobStatus_JOB_STATUS_DENIED {
		return p
	}
	// Clone packet/proto messages to avoid copying embedded mutex fields.
	out := packetCloneForFilter(p)
	if out == nil {
		wsQuarantineRedactionDrops.Inc()
		slog.Error("ws quarantine-redaction: clone failed, dropping broadcast",
			"job_id", jr.GetJobId(),
			"trace_id", sanitizeUTF8ForLog(strings.TrimSpace(p.GetTraceId())))
		return nil
	}
	sanitized := out.GetJobResult()
	if sanitized == nil {
		wsQuarantineRedactionDrops.Inc()
		slog.Error("ws quarantine-redaction: cloned packet has nil JobResult, dropping broadcast",
			"job_id", jr.GetJobId(),
			"trace_id", sanitizeUTF8ForLog(strings.TrimSpace(p.GetTraceId())))
		return nil
	}
	sanitized.ResultPtr = ""
	sanitized.ArtifactPtrs = nil
	return out
}

func (s *server) enqueueWSEvent(data []byte, tenant string, jobID string) (queued bool) {
	if s == nil || len(data) == 0 || s.eventsStopped.Load() {
		if len(data) > 0 {
			wsEventsDropped.WithLabelValues("stopped").Inc()
		}
		return false
	}
	defer func() {
		if r := recover(); r != nil {
			wsEventsDropped.WithLabelValues("closed").Inc()
			queued = false // channel closed between flag check and send; safe to ignore
		}
	}()
	select {
	case s.eventsCh <- wsEvent{data: data, tenant: strings.TrimSpace(tenant), jobID: strings.TrimSpace(jobID)}:
		return true
	default:
		wsEventsDropped.WithLabelValues("queue_full").Inc()
		return false
	}
}

func (s *server) handleWorkflowJobResult(ctx context.Context, jr *pb.JobResult) error {
	if s == nil || s.workflowEng == nil || jr == nil || jr.JobId == "" {
		return nil
	}
	runID, stepID := splitWorkflowJobID(jr.JobId)
	if runID == "" {
		return nil
	}

	if s.jobStore != nil {
		lockKey := "cordum:wf:run:lock:" + runID
		// Spin-wait up to 3 seconds for the run lock. The reconciler or
		// cancel handler may hold it briefly. Giving up too quickly causes
		// the message to bounce through NATS redelivery which is slower.
		lockDeadline := time.Now().Add(3 * time.Second)
		var token string
		for {
			var err error
			lockCtx, lockCancel := context.WithTimeout(ctx, 2*time.Second)
			token, err = s.jobStore.TryAcquireLock(lockCtx, lockKey, 30*time.Second)
			lockCancel()
			if err != nil {
				slog.Warn("workflow result: lock acquire error",
					"run_id", runID, "step_id", stepID, "job_id", jr.JobId, "error", err)
				return bus.RetryAfter(err, 1*time.Second)
			}
			if token != "" {
				break // acquired
			}
			if time.Now().After(lockDeadline) {
				// Couldn't acquire after 3s — check if stale before NATS retry
				if s.isStaleJobResult(ctx, runID, stepID, jr.JobId) {
					return nil // ACK — proven stale
				}
				slog.Warn("workflow result: run lock contended, deferring to NATS retry",
					"run_id", runID, "step_id", stepID, "job_id", jr.JobId,
					"lock_key", lockKey)
				return bus.RetryAfter(fmt.Errorf("run lock busy: %s", runID), 500*time.Millisecond)
			}
			time.Sleep(100 * time.Millisecond)
		}
		defer func() {
			releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if rErr := s.jobStore.ReleaseLock(releaseCtx, lockKey, token); rErr != nil {
				slog.Warn("workflow result: lock release failed, will expire via TTL",
					"run_id", runID, "lock_key", lockKey, "error", rErr)
			}
		}()
	}

	if err := s.workflowEng.HandleJobResult(ctx, jr); err != nil {
		if errors.Is(err, wf.ErrRunNotFound) {
			slog.Info("discarding job result for deleted/missing run",
				"component", "gateway", "run_id", runID,
				"step_id", stepID, "job_id", jr.JobId)
			return nil // ACK — run is gone, retrying won't help
		}
		return bus.RetryAfter(err, 1*time.Second)
	}
	return nil
}

// isStaleJobResult checks whether a job result message can be safely discarded
// because the target run is missing or in a terminal state, or the step is
// already terminal. This prevents orphan messages from creating infinite
// lock-contention retry storms against completed/deleted runs.
func (s *server) isStaleJobResult(ctx context.Context, runID, stepID, jobID string) bool {
	if s.workflowStore == nil {
		return false // can't check — keep retrying
	}
	run, err := s.workflowStore.GetRun(ctx, runID)
	if err != nil {
		// Run not found — it's stale. GetRun wraps redis.Nil, and the
		// engine wraps that as ErrRunNotFound. Check both.
		if errors.Is(err, redis.Nil) || errors.Is(err, wf.ErrRunNotFound) {
			slog.Info("discarding stale job result: run not found",
				"component", "gateway", "run_id", runID,
				"step_id", stepID, "job_id", jobID)
			return true
		}
		// Redis error — can't determine staleness, keep retrying
		return false
	}
	// Run is in a terminal state — no further results can affect it
	if wf.IsTerminalRunStatus(run.Status) {
		slog.Info("discarding stale job result: run is terminal",
			"component", "gateway", "run_id", runID,
			"step_id", stepID, "job_id", jobID,
			"run_status", string(run.Status))
		return true
	}
	// Check if the specific step is already terminal
	if stepID != "" && run.Steps != nil {
		if step, ok := run.Steps[stepID]; ok && step != nil {
			if step.Status == wf.StepStatusSucceeded || step.Status == wf.StepStatusFailed ||
				step.Status == wf.StepStatusCancelled || step.Status == wf.StepStatusTimedOut {
				slog.Info("discarding stale job result: step is terminal",
					"component", "gateway", "run_id", runID,
					"step_id", stepID, "job_id", jobID,
					"step_status", string(step.Status))
				return true
			}
		}
	}
	return false // run and step are still active — keep retrying
}

func splitWorkflowJobID(jobID string) (runID, stepID string) {
	parts := strings.SplitN(jobID, ":", 2)
	if len(parts) != 2 {
		return "", ""
	}
	runID = parts[0]
	stepID = parts[1]
	if at := strings.LastIndex(stepID, "@"); at > 0 {
		stepID = stepID[:at]
	}
	return runID, stepID
}

func (s *server) handleStream(w http.ResponseWriter, r *http.Request) {
	if s.auth != nil {
		if err := s.requireRole(r, "admin"); err != nil {
			writeForbidden(w, r, err)
			return
		}
	}
	if !s.requireLicensePermission(w, r, licensing.BreakGlassPermissionStreamRead) {
		return
	}

	slog.Info("ws connection attempt", "remote", r.RemoteAddr)
	ws, err := upgrader.Upgrade(w, r, negotiateSubprotocol(r))
	if err != nil {
		slog.Error("ws upgrade failed", "error", err)
		return
	}
	connStart := time.Now()
	// Capture timing values once to avoid data races if globals are
	// overwritten by tests after the handler goroutines have started.
	pingInterval, pongTimeout, revalidateInterval, revalidateRetryDelay := currentWSTimings()
	connID := newWSConnectionID()
	remoteIP := clientIP(r)
	disconnectState := &wsDisconnectState{}
	defer func() { _ = ws.Close() }()

	authCtx := auth.FromRequest(r)
	client := &wsClient{ch: make(chan wsEvent, s.wsClientBufSz)}
	if authCtx != nil {
		client.tenant = strings.TrimSpace(authCtx.Tenant)
		client.allowCrossTenant = authCtx.AllowCrossTenant
		client.apiKey = authCtx.APIKey
	}
	slog.Info("ws connected",
		"conn_id", connID,
		"remote", r.RemoteAddr,
		"tenant", client.tenant,
		"user_agent", r.UserAgent())
	s.clientsMu.Lock()
	s.clients[ws] = client
	s.clientsMu.Unlock()
	s.observeWSReconnect(remoteIP, connStart)
	wsClientsActive.Inc()
	s.statusCacheObj.Invalidate()
	s.startWSConnectionSummaryLogger()
	if err := setReadDeadlineOrError(ws, pingInterval+pongTimeout); err != nil {
		// The underlying socket is already compromised — limp-along with no
		// deadline would leave the read loop waiting indefinitely for a
		// frame that will never arrive. Close cleanly and let the client
		// reconnect. See task-1d4e6b4c bug #2.
		slog.Warn("ws initial read deadline failed; closing",
			"conn_id", connID,
			"remote", r.RemoteAddr,
			"tenant", client.tenant,
			"error", err)
		disconnectState.SetIfOneOf(disconnectClientClose, err, "", disconnectContextDone)
		_ = ws.Close()
		return
	}
	ws.SetPongHandler(func(string) error {
		wsPongsReceived.Inc()
		return ws.SetReadDeadline(time.Now().Add(pingInterval + pongTimeout))
	})
	wsCtx, wsCancel := context.WithCancel(r.Context())
	defer wsCancel()
	go func() {
		defer wsCancel()
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				if isWSReadTimeout(err) {
					wsPongTimeouts.Inc()
					disconnectState.SetIfOneOf(disconnectPingTimeout, err, "", disconnectClientClose, disconnectContextDone)
					return
				}
				if isShutdownSignaled(s.shutdownCh) {
					disconnectState.SetIfOneOf(disconnectServerDown, err, "", disconnectClientClose, disconnectContextDone)
					return
				}
				disconnectState.SetIfOneOf(disconnectClientClose, err, "", disconnectContextDone)
				return
			}
		}
	}()
	defer func() {
		s.rememberWSDisconnect(remoteIP, time.Now().UTC())
		wsClientsActive.Dec()
		wsConnectionDuration.Observe(time.Since(connStart).Seconds())
		s.statusCacheObj.Invalidate()
		reason, disconnectErr := disconnectState.Snapshot(disconnectContextDone)
		logArgs := []any{
			"conn_id", connID,
			"remote", r.RemoteAddr,
			"tenant", client.tenant,
			"duration", time.Since(connStart).Round(time.Millisecond),
			"reason", reason,
		}
		if disconnectErr != nil {
			logArgs = append(logArgs, "error", disconnectErr)
		}
		slog.Info("ws disconnected", logArgs...)
	}()
	defer func() {
		s.clientsMu.Lock()
		delete(s.clients, ws)
		s.clientsMu.Unlock()
		client.closeChannel()
	}()

	revalidate := time.NewTicker(revalidateInterval)
	defer revalidate.Stop()
	pingTicker := time.NewTicker(pingInterval)
	defer pingTicker.Stop()

	for {
		select {
		case msg, ok := <-client.ch:
			if !ok {
				disconnectState.SetIfOneOf(disconnectBufferFull, nil, "", disconnectClientClose, disconnectContextDone)
				return
			}
			if err := ws.SetWriteDeadline(time.Now().Add(wsWriteTimeout)); err != nil {
				disconnectState.SetIfOneOf(resolveWSWriteFailureReason(s.shutdownCh), err, "", disconnectClientClose, disconnectContextDone)
				return
			}
			if err := ws.WriteMessage(websocket.TextMessage, msg.data); err != nil {
				disconnectState.SetIfOneOf(resolveWSWriteFailureReason(s.shutdownCh), err, "", disconnectClientClose, disconnectContextDone)
				return
			}
		case <-revalidate.C:
			if s.auth != nil && client.apiKey != "" {
				if err := s.revalidateWSAuthWithRetry(wsCtx, client.apiKey, connID, revalidateRetryDelay); err != nil {
					disconnectState.Set(disconnectRevalidation, err)
					slog.Info("ws credential revoked, closing",
						"conn_id", connID,
						"tenant", client.tenant,
						"remote", r.RemoteAddr)
					if closeErr := ws.WriteControl(websocket.CloseMessage,
						websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "credential revoked"),
						time.Now().Add(2*time.Second)); closeErr != nil {
						slog.Warn("ws close control failed",
							"conn_id", connID,
							"tenant", client.tenant,
							"remote", r.RemoteAddr,
							"error", closeErr)
					}
					return
				}
			}
		case <-pingTicker.C:
			if err := ws.SetWriteDeadline(time.Now().Add(wsWriteTimeout)); err != nil {
				disconnectState.SetIfOneOf(resolveWSWriteFailureReason(s.shutdownCh), err, "", disconnectClientClose, disconnectContextDone)
				return
			}
			if err := ws.WriteMessage(websocket.PingMessage, nil); err != nil {
				disconnectState.SetIfOneOf(resolveWSWriteFailureReason(s.shutdownCh), err, "", disconnectClientClose, disconnectContextDone)
				return
			}
			wsPingsSent.Inc()
		case <-s.shutdownCh:
			disconnectState.Set(disconnectServerDown, nil)
			return
		case <-wsCtx.Done():
			switch {
			case isShutdownSignaled(s.shutdownCh):
				disconnectState.SetIfOneOf(disconnectServerDown, wsCtx.Err(), "")
			case r.Context().Err() != nil:
				disconnectState.SetIfOneOf(disconnectClientClose, wsCtx.Err(), "")
			default:
				disconnectState.SetIfOneOf(disconnectContextDone, wsCtx.Err(), "")
			}
			return
		}
	}
}

func (s *server) handleJobStream(w http.ResponseWriter, r *http.Request) {
	if s.jobStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "job store unavailable")
		return
	}
	jobID := strings.TrimSpace(r.PathValue("id"))
	if jobID == "" {
		writeErrorJSON(w, http.StatusBadRequest, "job id required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	tenant, err := s.jobStore.GetTenant(ctx, jobID)
	cancel()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			writeErrorJSON(w, http.StatusNotFound, "job not found")
			return
		}
		writeErrorJSON(w, http.StatusInternalServerError, "failed to resolve job tenant")
		return
	}
	if err := s.requireTenantAccess(r, tenant); err != nil {
		writeForbidden(w, r, err)
		return
	}

	slog.Info("job ws connection attempt", "job_id", jobID, "remote", r.RemoteAddr)
	ws, err := upgrader.Upgrade(w, r, negotiateSubprotocol(r))
	if err != nil {
		slog.Error("job ws upgrade failed", "job_id", jobID, "error", err)
		return
	}
	connStart := time.Now()
	connID := newWSConnectionID()
	remoteIP := clientIP(r)
	disconnectState := &wsDisconnectState{}
	defer func() { _ = ws.Close() }()

	authCtx := auth.FromRequest(r)
	client := &wsClient{ch: make(chan wsEvent, s.wsClientBufSz), tenant: strings.TrimSpace(tenant), jobID: jobID}
	if authCtx != nil {
		client.apiKey = authCtx.APIKey
	}
	slog.Info("job ws connected",
		"conn_id", connID,
		"job_id", jobID,
		"remote", r.RemoteAddr,
		"tenant", client.tenant,
		"user_agent", r.UserAgent())
	s.clientsMu.Lock()
	s.clients[ws] = client
	s.clientsMu.Unlock()
	s.observeWSReconnect(remoteIP, connStart)
	wsClientsActive.Inc()
	s.statusCacheObj.Invalidate()
	s.startWSConnectionSummaryLogger()
	pingInterval, pongTimeout, revalidateInterval, revalidateRetryDelay := currentWSTimings()
	if err := setReadDeadlineOrError(ws, pingInterval+pongTimeout); err != nil {
		// Compromised socket — close instead of running a deadline-less
		// read loop. See task-1d4e6b4c bug #2.
		slog.Warn("ws initial read deadline failed; closing",
			"conn_id", connID,
			"remote", r.RemoteAddr,
			"tenant", client.tenant,
			"job_id", jobID,
			"error", err)
		disconnectState.SetIfOneOf(disconnectClientClose, err, "", disconnectContextDone)
		_ = ws.Close()
		return
	}
	ws.SetPongHandler(func(string) error {
		wsPongsReceived.Inc()
		return ws.SetReadDeadline(time.Now().Add(pingInterval + pongTimeout))
	})
	wsCtx, wsCancel := context.WithCancel(r.Context())
	defer wsCancel()
	go func() {
		defer wsCancel()
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				if isWSReadTimeout(err) {
					wsPongTimeouts.Inc()
					disconnectState.SetIfOneOf(disconnectPingTimeout, err, "", disconnectClientClose, disconnectContextDone)
					return
				}
				if isShutdownSignaled(s.shutdownCh) {
					disconnectState.SetIfOneOf(disconnectServerDown, err, "", disconnectClientClose, disconnectContextDone)
					return
				}
				disconnectState.SetIfOneOf(disconnectClientClose, err, "", disconnectContextDone)
				return
			}
		}
	}()
	defer func() {
		s.rememberWSDisconnect(remoteIP, time.Now().UTC())
		wsClientsActive.Dec()
		wsConnectionDuration.Observe(time.Since(connStart).Seconds())
		s.statusCacheObj.Invalidate()
		reason, disconnectErr := disconnectState.Snapshot(disconnectContextDone)
		logArgs := []any{
			"conn_id", connID,
			"job_id", jobID,
			"remote", r.RemoteAddr,
			"tenant", client.tenant,
			"duration", time.Since(connStart).Round(time.Millisecond),
			"reason", reason,
		}
		if disconnectErr != nil {
			logArgs = append(logArgs, "error", disconnectErr)
		}
		slog.Info("job ws disconnected", logArgs...)
	}()
	defer func() {
		s.clientsMu.Lock()
		delete(s.clients, ws)
		s.clientsMu.Unlock()
		client.closeChannel()
	}()

	revalidate := time.NewTicker(revalidateInterval)
	defer revalidate.Stop()
	pingTicker := time.NewTicker(pingInterval)
	defer pingTicker.Stop()

	for {
		select {
		case msg, ok := <-client.ch:
			if !ok {
				disconnectState.SetIfOneOf(disconnectBufferFull, nil, "", disconnectClientClose, disconnectContextDone)
				return
			}
			if err := ws.SetWriteDeadline(time.Now().Add(wsWriteTimeout)); err != nil {
				disconnectState.SetIfOneOf(resolveWSWriteFailureReason(s.shutdownCh), err, "", disconnectClientClose, disconnectContextDone)
				return
			}
			if err := ws.WriteMessage(websocket.TextMessage, msg.data); err != nil {
				disconnectState.SetIfOneOf(resolveWSWriteFailureReason(s.shutdownCh), err, "", disconnectClientClose, disconnectContextDone)
				return
			}
		case <-revalidate.C:
			if s.auth != nil && client.apiKey != "" {
				if err := s.revalidateWSAuthWithRetry(wsCtx, client.apiKey, connID, revalidateRetryDelay); err != nil {
					disconnectState.Set(disconnectRevalidation, err)
					slog.Info("job ws credential revoked, closing",
						"conn_id", connID,
						"job_id", jobID,
						"tenant", client.tenant,
						"remote", r.RemoteAddr)
					if closeErr := ws.WriteControl(websocket.CloseMessage,
						websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "credential revoked"),
						time.Now().Add(2*time.Second)); closeErr != nil {
						slog.Warn("job ws close control failed",
							"conn_id", connID,
							"job_id", jobID,
							"tenant", client.tenant,
							"remote", r.RemoteAddr,
							"error", closeErr)
					}
					return
				}
			}
		case <-pingTicker.C:
			if err := ws.SetWriteDeadline(time.Now().Add(wsWriteTimeout)); err != nil {
				disconnectState.SetIfOneOf(resolveWSWriteFailureReason(s.shutdownCh), err, "", disconnectClientClose, disconnectContextDone)
				return
			}
			if err := ws.WriteMessage(websocket.PingMessage, nil); err != nil {
				disconnectState.SetIfOneOf(resolveWSWriteFailureReason(s.shutdownCh), err, "", disconnectClientClose, disconnectContextDone)
				return
			}
			wsPingsSent.Inc()
		case <-s.shutdownCh:
			disconnectState.Set(disconnectServerDown, nil)
			return
		case <-wsCtx.Done():
			switch {
			case isShutdownSignaled(s.shutdownCh):
				disconnectState.SetIfOneOf(disconnectServerDown, wsCtx.Err(), "")
			case r.Context().Err() != nil:
				disconnectState.SetIfOneOf(disconnectClientClose, wsCtx.Err(), "")
			default:
				disconnectState.SetIfOneOf(disconnectContextDone, wsCtx.Err(), "")
			}
			return
		}
	}
}

// revalidateWSAuth builds a synthetic HTTP request carrying the given API key
// and runs it through the configured auth provider. Returns nil if the key is
// still valid, or an error if revoked / expired.
func (s *server) revalidateWSAuth(apiKey string) error {
	req, _ := http.NewRequest(http.MethodGet, "/api/v1/stream", nil)
	req.Header.Set("X-API-Key", apiKey)
	_, err := s.auth.AuthenticateHTTP(req)
	return err
}

// revalidateWSAuthWithRetry re-checks a WebSocket client's API key against the
// auth provider. Returns nil when the key is still valid OR when ctx is
// cancelled (caller-initiated shutdown is not a failure). Returns a
// non-nil error in two situations: a non-transient failure (e.g. revoked
// credentials) is surfaced immediately, and the last transient error is
// surfaced after 3 exhausted retries.
//
// Callers MUST close the connection on any non-nil error. Tolerating
// stale auth for the full 2-minute revalidation window is unacceptable
// for a revoked or abused session — if the auth backend is transiently
// unreachable we drop the WS; the dashboard auto-reconnects and
// re-authenticates on its side. See task-1d4e6b4c bug #3.
func (s *server) revalidateWSAuthWithRetry(ctx context.Context, apiKey, connID string, retryDelay time.Duration) error {
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		err := s.revalidateWSAuth(apiKey)
		if err == nil {
			wsRevalidation.WithLabelValues("ok").Inc()
			return nil
		}
		if !isTransientError(err) {
			wsRevalidation.WithLabelValues("revoked").Inc()
			return err
		}
		wsRevalidation.WithLabelValues("transient_error").Inc()
		lastErr = err
		if attempt == 3 {
			slog.Error("ws auth revalidation transient failures exhausted; closing connection",
				"conn_id", connID,
				"attempts", attempt,
				"retry_delay", retryDelay,
				"error", err)
			return fmt.Errorf("ws auth revalidation exhausted %d transient retries: %w", attempt, err)
		}
		slog.Warn("ws auth revalidation transient failure; retrying",
			"conn_id", connID,
			"attempt", attempt,
			"max_attempts", 3,
			"retry_delay", retryDelay,
			"error", err)
		timer := time.NewTimer(retryDelay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil
		case <-timer.C:
		}
	}
	// Unreachable: the for-loop's attempt==3 branch returns. Kept for
	// defensive compilation — surface the last error if we somehow fall
	// through rather than silently returning nil.
	if lastErr != nil {
		return fmt.Errorf("ws auth revalidation exhausted retries: %w", lastErr)
	}
	return nil
}

func (s *server) observeWSReconnect(remoteIP string, now time.Time) {
	if s == nil || strings.TrimSpace(remoteIP) == "" {
		return
	}
	cutoff := now.Add(-wsReconnectWindow)
	s.recentWSDisconnects.Range(func(key, value any) bool {
		recordedAt, ok := value.(time.Time)
		if !ok || recordedAt.Before(cutoff) {
			s.recentWSDisconnects.Delete(key)
		}
		return true
	})

	if lastDisconnected, ok := s.recentWSDisconnects.LoadAndDelete(remoteIP); ok {
		if disconnectedAt, ok := lastDisconnected.(time.Time); ok && !disconnectedAt.Before(cutoff) {
			wsReconnections.Inc()
		}
	}
}

func (s *server) rememberWSDisconnect(remoteIP string, disconnectedAt time.Time) {
	if s == nil || strings.TrimSpace(remoteIP) == "" {
		return
	}
	if disconnectedAt.IsZero() {
		disconnectedAt = time.Now().UTC()
	}
	s.recentWSDisconnects.Store(remoteIP, disconnectedAt)
}

func (s *server) activeWSClientCount() int {
	if s == nil {
		return 0
	}
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()
	return len(s.clients)
}

func (s *server) startWSConnectionSummaryLogger() {
	if s == nil || s.shutdownCh == nil {
		return
	}
	s.wsSummaryOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(60 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					slog.Debug("ws connection summary", "active_ws_clients", s.activeWSClientCount())
				case <-s.shutdownCh:
					return
				}
			}
		}()
	})
}

func isShutdownSignaled(shutdownCh <-chan struct{}) bool {
	if shutdownCh == nil {
		return false
	}
	select {
	case <-shutdownCh:
		return true
	default:
		return false
	}
}

func resolveWSWriteFailureReason(shutdownCh <-chan struct{}) string {
	if isShutdownSignaled(shutdownCh) {
		return disconnectServerDown
	}
	return disconnectWriteError
}

func isWSReadTimeout(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func isTransientError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, redis.ErrClosed) ||
		errors.Is(err, net.ErrClosed) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}
