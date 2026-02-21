package bus

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cordum/cordum/core/infra/env"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

// NatsBus is a thin wrapper over a NATS connection that speaks protobuf packets.
type NatsBus struct {
	nc        *nats.Conn
	js        nats.JetStreamContext
	jsEnabled bool
	ackWait   time.Duration

	subsMu sync.Mutex
	subs   []*nats.Subscription

	// redis is an optional Redis client for crash-safe message processing.
	// When set, durable JetStream subscriptions use Redis for idempotency
	// guards and in-flight tracking. When nil, degrades to JetStream-only semantics.
	redis redis.UniversalClient

	// OnMessageTerminated is called when a message is about to be permanently
	// terminated (poison pill or corrupt payload). Callers should use this to
	// route the message to a dead-letter queue. If the callback returns an error
	// (e.g. DLQ write failed), the message is Nak'd for retry instead of
	// terminated, preventing permanent data loss.
	OnMessageTerminated func(subject string, data []byte, numDelivered uint64) error
}

const (
	envUseJetStream      = "NATS_USE_JETSTREAM"
	envJSAckWait         = "NATS_JS_ACK_WAIT"
	envJSMaxAge          = "NATS_JS_MAX_AGE"
	envJSReplicas        = "NATS_JS_REPLICAS"
	envNATSTLSCA         = "NATS_TLS_CA"
	envNATSTLSCert       = "NATS_TLS_CERT"
	envNATSTLSKey        = "NATS_TLS_KEY"
	envNATSTLSInsecure   = "NATS_TLS_INSECURE"
	envNATSTLSServerName = "NATS_TLS_SERVER_NAME"

	defaultAckWait = 10 * time.Minute
	defaultMaxAge  = 7 * 24 * time.Hour

	streamSys  = "CORDUM_SYS"
	streamJobs = "CORDUM_JOBS"

	// LabelBusMsgID overrides JetStream msg-id for explicit resubmits.
	LabelBusMsgID = "cordum.bus_msg_id"

	// maxJSRedeliveries caps how many times NATS redelivers a failing message.
	// Without this, a poison message (e.g. proto unmarshal failure) blocks the
	// MaxAckPending budget indefinitely, preventing new messages from being delivered.
	maxJSRedeliveries = 100
)

func natsMaxAckPending() int {
	const defaultMaxAckPending = 2048
	if raw := strings.TrimSpace(os.Getenv("NATS_MAX_ACK_PENDING")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			return v
		}
	}
	return defaultMaxAckPending
}

var (
	errNilBus     = errors.New("nats bus not initialized")
	errNilPacket  = errors.New("nil bus packet")
	errEmptyTopic = errors.New("empty subject")
)

// NewNatsBus dials NATS at the provided URL.
// TLS env vars (NATS_TLS_CA, etc.) are only applied when the URL uses the
// tls:// scheme, so plain nats:// connections (e.g. embedded NATS in tests)
// are not affected by ambient TLS environment variables.
func NewNatsBus(url string) (*NatsBus, error) {
	opts := []nats.Option{
		nats.Name("cordum-bus"),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2 * time.Second),
		nats.DisconnectErrHandler(func(nc *nats.Conn, err error) {
			log.Printf("[BUS] disconnected from NATS: %v", err)
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			log.Printf("[BUS] reconnected to NATS at %s", nc.ConnectedUrl())
		}),
		nats.ClosedHandler(func(nc *nats.Conn) {
			log.Printf("[BUS] connection closed")
		}),
	}
	if strings.HasPrefix(url, "tls://") {
		if tlsConfig, err := natsTLSConfigFromEnv(); err != nil {
			return nil, fmt.Errorf("nats tls config: %w", err)
		} else if tlsConfig != nil {
			opts = append(opts, nats.Secure(tlsConfig))
		}
	}

	nc, err := nats.Connect(url, opts...)
	if err != nil {
		return nil, fmt.Errorf("connect nats %s: %w", url, err)
	}
	b := &NatsBus{nc: nc, ackWait: defaultAckWait}
	b.initJetStreamFromEnv()
	return b, nil
}

// WithRedis sets an optional Redis client for crash-safe message processing.
// When set, durable JetStream subscriptions use Redis-backed idempotency guards
// to prevent duplicate processing after crash/restart. Call before Subscribe.
func (b *NatsBus) WithRedis(client redis.UniversalClient) *NatsBus {
	b.redis = client
	return b
}

const (
	// processedKeyPrefix is the Redis key prefix for idempotency tracking.
	// Key format: cordum:bus:processed:<stream>:<seq>
	processedKeyPrefix = "cordum:bus:processed:"

	// processedKeyTTL matches JetStream AckWait — after this, NATS won't
	// redeliver anyway, so the dedup key can expire.
	processedKeyTTL = 10 * time.Minute

	// inflightKeyPrefix tracks messages currently being processed.
	// Key format: cordum:bus:inflight:<stream>:<seq>
	inflightKeyPrefix = "cordum:bus:inflight:"

	// inflightKeyTTL is a safety bound — if a replica crashes mid-processing,
	// the key expires and stops polluting the keyspace.
	inflightKeyTTL = 2 * time.Minute
)

// processedKey returns the Redis key for tracking a processed message.
func processedKey(stream string, seq uint64) string {
	return processedKeyPrefix + stream + ":" + strconv.FormatUint(seq, 10)
}

// inflightKey returns the Redis key for tracking an in-flight message.
func inflightKey(stream string, seq uint64) string {
	return inflightKeyPrefix + stream + ":" + strconv.FormatUint(seq, 10)
}

// Drain unsubscribes all tracked subscriptions, allowing in-flight messages
// to complete. Call before Close() to avoid orphaned JetStream consumers.
func (b *NatsBus) Drain() {
	b.subsMu.Lock()
	subs := b.subs
	b.subs = nil
	b.subsMu.Unlock()

	for _, sub := range subs {
		if sub == nil || !sub.IsValid() {
			continue
		}
		if err := sub.Drain(); err != nil {
			log.Printf("[BUS] drain subscription %s: %v", sub.Subject, err)
		}
	}
}

// Close drains all subscriptions and shuts down the underlying NATS connection.
func (b *NatsBus) Close() {
	b.Drain()
	if b.nc != nil {
		b.nc.Close()
	}
}

// DirectSubject constructs a worker-specific subject for targeted delivery.
func DirectSubject(workerID string) string {
	if workerID == "" {
		return ""
	}
	return fmt.Sprintf("worker.%s.jobs", workerID)
}

// Publish sends a protobuf-encoded BusPacket on the given subject.
func (b *NatsBus) Publish(subject string, packet *pb.BusPacket) error {
	if b == nil || b.nc == nil {
		return errNilBus
	}
	if subject == "" {
		return errEmptyTopic
	}
	if packet == nil {
		return errNilPacket
	}
	data, err := proto.Marshal(packet)
	if err != nil {
		return fmt.Errorf("marshal bus packet: %w", err)
	}
	if b != nil && b.jsEnabled && isDurableSubject(subject) {
		msgID := computeMsgID(subject, packet)
		if msgID != "" {
			_, err = b.js.Publish(subject, data, nats.MsgId(msgID))
		} else {
			_, err = b.js.Publish(subject, data)
		}
		if err != nil {
			return fmt.Errorf("publish %s: %w", subject, err)
		}
		return nil
	}
	if err := b.nc.Publish(subject, data); err != nil {
		return fmt.Errorf("publish %s: %w", subject, err)
	}
	return nil
}

// Subscribe attaches a subscription that decodes protobuf packets and invokes the handler.
// When JetStream is enabled, durable subjects are consumed with explicit ack/nak semantics.
func (b *NatsBus) Subscribe(subject, queue string, handler func(*pb.BusPacket) error) error {
	if b == nil || b.nc == nil {
		return errNilBus
	}
	if subject == "" {
		return errEmptyTopic
	}
	if handler == nil {
		return errors.New("nil handler")
	}
	if b != nil && b.jsEnabled && isDurableSubject(subject) {
		cb := func(msg *nats.Msg) {
			var numDelivered uint64
			var streamName string
			var streamSeq uint64
			if meta, metaErr := msg.Metadata(); metaErr == nil {
				numDelivered = meta.NumDelivered
				streamName = meta.Stream
				streamSeq = meta.Sequence.Stream
				// Terminate messages that have reached max delivery — they are poison pills
				// blocking the queue for all messages behind them.
				if numDelivered >= uint64(maxJSRedeliveries) {
					log.Printf("nats bus: TERMINATING poison message on %s after %d deliveries (stream_seq=%d consumer_seq=%d)",
						subject, numDelivered, meta.Sequence.Stream, meta.Sequence.Consumer)
					// DLQ write BEFORE Term — prevents data loss if we crash between Term and DLQ write.
					if b.OnMessageTerminated != nil {
						if dlqErr := b.OnMessageTerminated(subject, msg.Data, numDelivered); dlqErr != nil {
							log.Printf("nats bus: DLQ write failed, nak-ing for retry: %v", dlqErr)
							_ = msg.NakWithDelay(5 * time.Second)
							return
						}
					}
					if termErr := msg.Term(); termErr != nil {
						log.Printf("nats bus: term failed: %v", termErr)
					}
					return
				}
				if numDelivered > 50 {
					log.Printf("nats bus: message on %s redelivered %d times (max=%d)", subject, numDelivered, maxJSRedeliveries)
				}
			}

			// Idempotency guard: skip processing if already handled by another replica.
			// This covers the crash window: replica A processes → crash before Ack → redelivery to B.
			if b.redis != nil && streamSeq > 0 {
				pKey := processedKey(streamName, streamSeq)
				rCtx, rCancel := context.WithTimeout(context.Background(), 2*time.Second)
				exists, err := b.redis.Exists(rCtx, pKey).Result()
				rCancel()
				if err != nil {
					log.Printf("nats bus: idempotency check failed, nak-ing for retry: %v", err)
					_ = msg.NakWithDelay(2 * time.Second)
					return
				} else if exists > 0 {
					// Already processed — just Ack and skip.
					if ackErr := msg.Ack(); ackErr != nil {
						log.Printf("nats bus: ack (dedup) failed: %v", ackErr)
					}
					return
				}
			}

			// Mark in-flight for observability.
			if b.redis != nil && streamSeq > 0 {
				iKey := inflightKey(streamName, streamSeq)
				rCtx, rCancel := context.WithTimeout(context.Background(), 2*time.Second)
				_ = b.redis.Set(rCtx, iKey, "1", inflightKeyTTL).Err()
				rCancel()
			}

			action, delay := processBusMsg(msg.Data, handler, numDelivered)

			// Clear in-flight tracking after processing.
			if b.redis != nil && streamSeq > 0 {
				iKey := inflightKey(streamName, streamSeq)
				rCtx, rCancel := context.WithTimeout(context.Background(), 2*time.Second)
				_ = b.redis.Del(rCtx, iKey).Err()
				rCancel()
			}

			switch action {
			case msgActionTerm:
				log.Printf("nats bus: terminating non-retryable message on %s (deliveries=%d)", subject, numDelivered)
				// DLQ write BEFORE Term — prevents data loss if we crash between Term and DLQ write.
				if b.OnMessageTerminated != nil {
					if dlqErr := b.OnMessageTerminated(subject, msg.Data, numDelivered); dlqErr != nil {
						log.Printf("nats bus: DLQ write failed, nak-ing for retry: %v", dlqErr)
						_ = msg.NakWithDelay(5 * time.Second)
						break
					}
				}
				if termErr := msg.Term(); termErr != nil {
					log.Printf("nats bus: term failed: %v", termErr)
				}
			case msgActionNakDelay:
				log.Printf("nats bus: nak-with-delay on %s (delay=%v)", subject, delay)
				if nakErr := msg.NakWithDelay(delay); nakErr != nil {
					log.Printf("nats bus: nak-with-delay failed: %v", nakErr)
				}
			case msgActionNak:
				if nakErr := msg.Nak(); nakErr != nil {
					log.Printf("nats bus: nak failed: %v", nakErr)
				}
			default:
				// Mark as processed BEFORE Ack so redelivery finds the guard.
				if b.redis != nil && streamSeq > 0 {
					pKey := processedKey(streamName, streamSeq)
					rCtx, rCancel := context.WithTimeout(context.Background(), 2*time.Second)
					if setErr := b.redis.Set(rCtx, pKey, "1", processedKeyTTL).Err(); setErr != nil {
						log.Printf("nats bus: idempotency set failed, nak-ing for retry: %v", setErr)
						rCancel()
						_ = msg.NakWithDelay(2 * time.Second)
						return
					}
					rCancel()
				}
				if ackErr := msg.Ack(); ackErr != nil {
					log.Printf("nats bus: ack failed: %v", ackErr)
				}
			}
		}

		opts := []nats.SubOpt{
			nats.ManualAck(),
			nats.AckExplicit(),
			nats.AckWait(b.ackWait),
			nats.MaxAckPending(natsMaxAckPending()),
			nats.MaxDeliver(maxJSRedeliveries),
		}
		if durable := durableName(subject, queue); durable != "" {
			opts = append(opts, nats.Durable(durable))
		}

		var (
			sub *nats.Subscription
			err error
		)
		if queue == "" {
			sub, err = b.js.Subscribe(subject, cb, opts...)
		} else {
			sub, err = b.js.QueueSubscribe(subject, queue, cb, opts...)
		}
		if err != nil {
			return fmt.Errorf("subscribe %s: %w", subject, err)
		}
		b.trackSub(sub)
		return nil
	}

	cb := func(msg *nats.Msg) {
		var packet pb.BusPacket
		if err := proto.Unmarshal(msg.Data, &packet); err != nil {
			log.Printf("nats bus: failed to unmarshal packet: %v", err)
			return
		}
		if err := handler(&packet); err != nil {
			log.Printf("nats bus: handler error: %v", err)
		}
	}
	if queue == "" {
		sub, err := b.nc.Subscribe(subject, cb)
		if err != nil {
			return fmt.Errorf("subscribe %s: %w", subject, err)
		}
		b.trackSub(sub)
		return nil
	}
	sub, err := b.nc.QueueSubscribe(subject, queue, cb)
	if err != nil {
		return fmt.Errorf("subscribe %s: %w", subject, err)
	}
	b.trackSub(sub)
	return nil
}

// trackSub appends a subscription to the tracked list.
func (b *NatsBus) trackSub(sub *nats.Subscription) {
	if sub == nil {
		return
	}
	b.subsMu.Lock()
	b.subs = append(b.subs, sub)
	b.subsMu.Unlock()
}

// msgAction represents the action to take after processing a NATS message.
type msgAction int

const (
	msgActionAck      msgAction = iota // Acknowledge (processed or non-retryable error)
	msgActionNak                       // Retry immediately
	msgActionNakDelay                  // Retry after delay (poison pill or retryable error)
	msgActionTerm                      // Terminate redelivery (permanent failure, e.g. corrupt payload)
)

// poisonUnmarshalThreshold is the number of delivery attempts after which an
// unmarshal failure is treated as permanent corruption rather than transient.
const poisonUnmarshalThreshold uint64 = 3

// processBusMsg unmarshals raw message data, invokes the handler, and returns
// the action to take plus an optional NAK delay. numDelivered is the JetStream
// delivery count (0 when metadata is unavailable).
func processBusMsg(data []byte, handler func(*pb.BusPacket) error, numDelivered uint64) (msgAction, time.Duration) {
	var packet pb.BusPacket
	if err := proto.Unmarshal(data, &packet); err != nil {
		// If unmarshal fails after multiple redeliveries, the payload is
		// permanently corrupt. Terminate to prevent queue starvation.
		if numDelivered > poisonUnmarshalThreshold {
			return msgActionTerm, 0
		}
		return msgActionNakDelay, 5 * time.Second
	}
	if err := handler(&packet); err != nil {
		if delay, ok := RetryDelay(err); ok {
			if delay > 0 {
				return msgActionNakDelay, delay
			}
			return msgActionNak, 0
		}
		return msgActionAck, 0
	}
	return msgActionAck, 0
}

func (b *NatsBus) IsConnected() bool {
	return b != nil && b.nc != nil && b.nc.IsConnected()
}

func (b *NatsBus) Status() string {
	if b == nil || b.nc == nil {
		return "UNKNOWN"
	}
	return b.nc.Status().String()
}

func (b *NatsBus) ConnectedURL() string {
	if b == nil || b.nc == nil {
		return ""
	}
	return b.nc.ConnectedUrl()
}

func initJetStreamEnabled() bool {
	val := strings.TrimSpace(os.Getenv(envUseJetStream))
	if val == "" {
		return false
	}
	switch strings.ToLower(val) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func natsTLSConfigFromEnv() (*tls.Config, error) {
	caPath := strings.TrimSpace(os.Getenv(envNATSTLSCA))
	certPath := strings.TrimSpace(os.Getenv(envNATSTLSCert))
	keyPath := strings.TrimSpace(os.Getenv(envNATSTLSKey))
	serverName := strings.TrimSpace(os.Getenv(envNATSTLSServerName))
	insecure := parseBoolEnv(envNATSTLSInsecure)
	production := env.IsProduction()

	if caPath == "" && certPath == "" && keyPath == "" && serverName == "" && !insecure {
		if production {
			return nil, fmt.Errorf("nats tls required in production")
		}
		return nil, nil
	}

	if production && insecure {
		return nil, fmt.Errorf("nats tls insecure not allowed in production")
	}

	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if env.TLSMinVersion() == tls.VersionTLS13 {
		cfg.MinVersion = tls.VersionTLS13
	}
	if serverName != "" {
		cfg.ServerName = serverName
	}
	if insecure {
		cfg.InsecureSkipVerify = true
	}
	if caPath != "" {
		pem, err := os.ReadFile(caPath) // #nosec -- CA path is configured by the operator.
		if err != nil {
			return nil, fmt.Errorf("nats tls ca read: %w", err)
		}
		pool := x509.NewCertPool()
		if ok := pool.AppendCertsFromPEM(pem); !ok {
			return nil, fmt.Errorf("nats tls ca parse: %s", caPath)
		}
		cfg.RootCAs = pool
	}
	if certPath != "" || keyPath != "" {
		if certPath == "" || keyPath == "" {
			return nil, fmt.Errorf("nats tls cert/key must be set together")
		}
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return nil, fmt.Errorf("nats tls keypair: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}

func parseBoolEnv(key string) bool {
	val := strings.TrimSpace(os.Getenv(key))
	if val == "" {
		return false
	}
	switch strings.ToLower(val) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func (b *NatsBus) initJetStreamFromEnv() {
	if b == nil || b.nc == nil {
		return
	}
	if !initJetStreamEnabled() {
		return
	}
	ackWait := defaultAckWait
	if v := strings.TrimSpace(os.Getenv(envJSAckWait)); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			ackWait = d
		}
	}
	maxAge := defaultMaxAge
	if v := strings.TrimSpace(os.Getenv(envJSMaxAge)); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			maxAge = d
		}
	}
	replicas := 1
	if v := strings.TrimSpace(os.Getenv(envJSReplicas)); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			replicas = parsed
		}
	}

	js, err := b.nc.JetStream()
	if err != nil {
		log.Printf("[BUS] jetstream init failed: %v", err)
		return
	}
	if _, err := js.AccountInfo(); err != nil {
		log.Printf("[BUS] jetstream not available: %v", err)
		return
	}

	// Ensure streams exist (best-effort).
	ensureStream := func(name string, subjects []string) {
		_, err := js.AddStream(&nats.StreamConfig{
			Name:       name,
			Subjects:   subjects,
			Retention:  nats.LimitsPolicy,
			Storage:    nats.FileStorage,
			MaxAge:     maxAge,
			Replicas:   replicas,
			Duplicates: 2 * time.Minute,
		})
		if err == nil {
			log.Printf("[BUS] jetstream stream ensured name=%s subjects=%v max_age=%s", name, subjects, maxAge)
			return
		}
		// Stream may already exist; treat that as success.
		if _, infoErr := js.StreamInfo(name); infoErr == nil {
			return
		}
		log.Printf("[BUS] jetstream ensure stream failed name=%s: %v", name, err)
	}
	ensureStream(streamSys, []string{"sys.>"})
	ensureStream(streamJobs, []string{"job.>", "worker.*.jobs"})

	b.js = js
	b.jsEnabled = true
	b.ackWait = ackWait
	log.Printf("[BUS] jetstream enabled ack_wait=%s replicas=%d", ackWait, replicas) // #nosec -- values are derived from configuration.
}

// isDurableSubject returns true for subjects that need JetStream persistence.
//
// Broadcast subjects (sys.heartbeat, sys.handshake, sys.config.changed, sys.alert,
// sys.job.progress, sys.workflow.event) intentionally use core NATS, NOT JetStream.
// Each has built-in resilience that makes at-most-once delivery safe:
//   - sys.heartbeat: workers re-heartbeat every 5-10s, so a missed message self-heals.
//   - sys.config.changed: 30s poll fallback in config_overlay.go catches missed notifications.
//   - sys.handshake: workers re-register on the next heartbeat cycle.
//   - sys.alert, sys.job.progress, sys.workflow.event: informational, no state dependency.
//
// If a new JetStream broadcast subject is added, consider ephemeral consumer behavior
// during rolling restarts: ephemeral consumers are deleted when disconnected, so messages
// published between disconnect and reconnect are lost.
func isDurableSubject(subject string) bool {
	switch subject {
	case capsdk.SubjectSubmit, capsdk.SubjectResult, capsdk.SubjectDLQ, capsdk.SubjectAuditExport:
		return true
	}
	if strings.HasPrefix(subject, "job.") {
		return true
	}
	if strings.HasPrefix(subject, "worker.") && strings.HasSuffix(subject, ".jobs") {
		return true
	}
	return false
}

func durableName(subject, queue string) string {
	name := strings.ReplaceAll(subject, ".", "_")
	name = strings.ReplaceAll(name, "*", "STAR")
	name = strings.ReplaceAll(name, ">", "GT")
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if queue == "" {
		// Broadcast subscriptions must use ephemeral consumers so each replica
		// gets its own consumer and receives all messages. A shared durable name
		// would make JetStream deliver each message to only one replica.
		return ""
	}
	q := strings.ReplaceAll(queue, ".", "_")
	q = strings.ReplaceAll(q, "*", "STAR")
	q = strings.ReplaceAll(q, ">", "GT")
	q = strings.TrimSpace(q)
	if q == "" {
		return "dur_" + name
	}
	return "dur_" + q + "__" + name
}

func computeMsgID(subject string, packet *pb.BusPacket) string {
	if packet == nil {
		return ""
	}
	id := ""
	prefix := subject + ":"
	switch payload := packet.Payload.(type) {
	case *pb.BusPacket_JobRequest:
		if payload.JobRequest != nil {
			if payload.JobRequest.Labels != nil {
				if override := strings.TrimSpace(payload.JobRequest.Labels[LabelBusMsgID]); override != "" {
					return "jobreq:" + subject + ":" + override
				}
			}
			id = payload.JobRequest.JobId
			prefix = "jobreq:"
		}
	case *pb.BusPacket_JobResult:
		if payload.JobResult != nil {
			id = payload.JobResult.JobId
		}
	case *pb.BusPacket_Heartbeat:
		if payload.Heartbeat != nil {
			id = payload.Heartbeat.WorkerId
		}
	case *pb.BusPacket_Handshake:
		if payload.Handshake != nil {
			id = payload.Handshake.ComponentId
			prefix = "handshake:"
		}
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	return prefix + id
}
