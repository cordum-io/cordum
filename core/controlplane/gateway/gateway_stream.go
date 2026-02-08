package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cordum/cordum/core/infra/logging"
	"github.com/cordum/cordum/core/infra/memory"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/encoding/protojson"
)

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

type wsClient struct {
	ch               chan wsEvent
	tenant           string
	allowCrossTenant bool
	jobID            string
}

type wsEvent struct {
	data   []byte
	tenant string
	jobID  string
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return isAllowedOrigin(r) },
}

// negotiateSubprotocol builds a response header that echoes back the client's
// cordum-api-key subprotocol so browsers accept the upgrade handshake.
func negotiateSubprotocol(r *http.Request) http.Header {
	for _, p := range websocket.Subprotocols(r) {
		if strings.HasPrefix(strings.ToLower(p), strings.ToLower(wsAPIKeyProtocol)) {
			return http.Header{"Sec-Websocket-Protocol": {p}}
		}
	}
	return nil
}

// startBusTaps subscribes to heartbeats and system events once for the lifetime of the gateway.
func (s *server) startBusTaps() error {
	// Heartbeats -> worker registry snapshot
	if err := s.bus.Subscribe(capsdk.SubjectHeartbeat, "", func(p *pb.BusPacket) error {
		if hb := p.GetHeartbeat(); hb != nil {
			s.workerMu.Lock()
			s.workers[hb.WorkerId] = hb
			s.workerMu.Unlock()
			// Also stream heartbeats to WS listeners (best effort).
			s.enqueueBusPacket(p)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("subscribe %s: %w", capsdk.SubjectHeartbeat, err)
	}

	// DLQ tap to persist entries
	if s.dlqStore != nil {
		if err := s.bus.Subscribe(capsdk.SubjectDLQ, "", func(p *pb.BusPacket) error {
			if jr := p.GetJobResult(); jr != nil {
				jobID := strings.TrimSpace(jr.JobId)
				topic := ""
				lastState := ""
				attempts := 0
				if s.jobStore != nil && jobID != "" {
					if t, err := s.jobStore.GetTopic(context.Background(), jobID); err == nil {
						topic = t
					}
					if st, err := s.jobStore.GetState(context.Background(), jobID); err == nil {
						lastState = string(st)
					}
					if a, err := s.jobStore.GetAttempts(context.Background(), jobID); err == nil {
						attempts = a
					}
				}
				_ = s.dlqStore.Add(context.Background(), memory.DLQEntry{
					JobID:      jobID,
					Topic:      topic,
					Status:     jr.Status.String(),
					Reason:     jr.ErrorMessage,
					ReasonCode: strings.TrimSpace(jr.ErrorCode),
					LastState:  lastState,
					Attempts:   attempts,
					CreatedAt:  time.Now().UTC(),
				})

				// Best effort: ensure a result exists for failed-to-dispatch jobs so clients can inspect `res:<job_id>`.
				if s.memStore != nil && s.jobStore != nil && jobID != "" {
					resKey := memory.MakeResultKey(jobID)
					resPtr := memory.PointerForKey(resKey)
					body := map[string]any{
						"job_id":       jobID,
						"status":       jr.Status.String(),
						"error":        map[string]any{"message": jr.ErrorMessage},
						"processed_by": "cordum-scheduler",
						"completed_at": time.Now().UTC().Format(time.RFC3339),
					}
					if data, err := json.Marshal(body); err == nil {
						_ = s.memStore.PutResult(context.Background(), resKey, data)
					}
					if existing, err := s.jobStore.GetResultPtr(context.Background(), jobID); err != nil || strings.TrimSpace(existing) == "" {
						_ = s.jobStore.SetResultPtr(context.Background(), jobID, resPtr)
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
			if subject == "sys.job.>" {
				s.handleWorkflowJobResult(context.Background(), p.GetJobResult())
			}
			s.enqueueBusPacket(p)
			return nil
		}); err != nil {
			logging.Error("api-gateway", "bus subscribe failed", "subject", subject, "error", err)
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
					delete(s.clients, conn)
				}
				s.clientsMu.Unlock()

				for _, conn := range slowClients {
					if err := conn.Close(); err != nil {
						logging.Error("api-gateway", "ws client close failed", "error", err)
					}
				}
			case <-s.shutdownCh:
				return
			}
		}
	}()

	return nil
}

func (s *server) enqueueBusPacket(p *pb.BusPacket) {
	if s == nil || p == nil {
		return
	}
	data, err := protojson.Marshal(p)
	if err != nil {
		logging.Error("api-gateway", "protojson marshal failed", "error", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	tenant, _ := s.tenantForBusPacket(ctx, p)
	cancel()
	jobID := jobIDForBusPacket(p)
	s.enqueueWSEvent(data, tenant, jobID)
}

func (s *server) enqueueWSEvent(data []byte, tenant string, jobID string) {
	if s == nil || len(data) == 0 || s.eventsStopped.Load() {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			_ = r // channel closed between flag check and send; safe to ignore
		}
	}()
	select {
	case s.eventsCh <- wsEvent{data: data, tenant: strings.TrimSpace(tenant), jobID: strings.TrimSpace(jobID)}:
	default:
	}
}

func (s *server) handleWorkflowJobResult(ctx context.Context, jr *pb.JobResult) {
	if s == nil || s.workflowEng == nil || jr == nil || jr.JobId == "" {
		return
	}
	runID, _ := splitWorkflowJobID(jr.JobId)
	if runID == "" {
		return
	}

	if s.jobStore != nil {
		lockKey := "cordum:wf:run:lock:" + runID
		ok, err := s.jobStore.TryAcquireLock(ctx, lockKey, 30*time.Second)
		if err != nil || !ok {
			return
		}
		defer func() { _ = s.jobStore.ReleaseLock(context.Background(), lockKey) }()
	}

	s.workflowEng.HandleJobResult(ctx, jr)
}

func splitWorkflowJobID(jobID string) (runID, stepID string) {
	parts := strings.Split(jobID, ":")
	if len(parts) < 2 {
		return "", ""
	}
	runID = strings.Join(parts[:len(parts)-1], ":")
	stepID = parts[len(parts)-1]
	return runID, stepID
}

func (s *server) handleStream(w http.ResponseWriter, r *http.Request) {
	if s.auth != nil {
		if err := s.requireRole(r, "admin"); err != nil {
			writeErrorJSON(w, http.StatusForbidden, err.Error())
			return
		}
	}

	logging.Info("gateway", "ws connection attempt", "remote", r.RemoteAddr)
	ws, err := upgrader.Upgrade(w, r, negotiateSubprotocol(r))
	if err != nil {
		logging.Error("gateway", "ws upgrade failed", "error", err)
		return
	}
	defer ws.Close()
	logging.Info("gateway", "ws connected", "remote", r.RemoteAddr)

	authCtx := authFromRequest(r)
	client := &wsClient{ch: make(chan wsEvent, 100)}
	if authCtx != nil {
		client.tenant = strings.TrimSpace(authCtx.Tenant)
		client.allowCrossTenant = authCtx.AllowCrossTenant
	}
	s.clientsMu.Lock()
	s.clients[ws] = client
	s.clientsMu.Unlock()
	defer func() {
		s.clientsMu.Lock()
		delete(s.clients, ws)
		s.clientsMu.Unlock()
		close(client.ch)
	}()

	for {
		select {
		case msg, ok := <-client.ch:
			if !ok {
				return
			}
			_ = ws.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
			if err := ws.WriteMessage(websocket.TextMessage, msg.data); err != nil {
				return
			}
		case <-r.Context().Done():
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
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}

	logging.Info("gateway", "job ws connection attempt", "job_id", jobID, "remote", r.RemoteAddr)
	ws, err := upgrader.Upgrade(w, r, negotiateSubprotocol(r))
	if err != nil {
		logging.Error("gateway", "job ws upgrade failed", "job_id", jobID, "error", err)
		return
	}
	defer ws.Close()
	logging.Info("gateway", "job ws connected", "job_id", jobID, "remote", r.RemoteAddr)

	client := &wsClient{ch: make(chan wsEvent, 100), tenant: strings.TrimSpace(tenant), jobID: jobID}
	s.clientsMu.Lock()
	s.clients[ws] = client
	s.clientsMu.Unlock()
	defer func() {
		s.clientsMu.Lock()
		delete(s.clients, ws)
		s.clientsMu.Unlock()
		close(client.ch)
	}()

	for {
		select {
		case msg, ok := <-client.ch:
			if !ok {
				return
			}
			_ = ws.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
			if err := ws.WriteMessage(websocket.TextMessage, msg.data); err != nil {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}
