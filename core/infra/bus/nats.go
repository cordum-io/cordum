package bus

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
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

	hooksMu            sync.RWMutex
	reconnectHandlers  []func(*nats.Conn)
	disconnectHandlers []func(*nats.Conn, error)

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
	envNATSAllowPlain    = "CORDUM_NATS_ALLOW_PLAINTEXT"
	envNATSUsername      = "NATS_USERNAME"
	envNATSPassword      = "NATS_PASSWORD"
	envNATSNKey          = "NATS_NKEY"
	envNATSToken         = "NATS_TOKEN"
	envNATSPingInterval  = "NATS_PING_INTERVAL"

	defaultAckWait          = 10 * time.Minute
	defaultMaxAge           = 7 * 24 * time.Hour
	defaultNATSPingInterval = 30 * time.Second

	streamSys  = "CORDUM_SYS"
	streamJobs = "CORDUM_JOBS"

	// LabelBusMsgID overrides JetStream msg-id for explicit resubmits.
	LabelBusMsgID = "cordum.bus_msg_id"

	// maxJSRedeliveries caps how many times NATS redelivers a failing message.
	// Without this, a poison message (e.g. proto unmarshal failure) blocks the
	// MaxAckPending budget indefinitely, preventing new messages from being delivered.
	maxJSRedeliveries = 100
)

const maxAckPendingHardLimit = 50000

func natsMaxAckPending() int {
	const defaultMaxAckPending = 2048
	if raw := strings.TrimSpace(os.Getenv("NATS_MAX_ACK_PENDING")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			if v > maxAckPendingHardLimit {
				slog.Warn("bus: NATS_MAX_ACK_PENDING exceeds hard limit, clamping",
					"requested", v, "clamped_to", maxAckPendingHardLimit)
				return maxAckPendingHardLimit
			}
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
//
// In production mode (CORDUM_ENV=production or CORDUM_PRODUCTION=true),
// non-TLS URLs are rejected unless CORDUM_NATS_ALLOW_PLAINTEXT=true.
// Authentication is configured via NATS_USERNAME/NATS_PASSWORD, NATS_TOKEN,
// or NATS_NKEY env vars. A warning is logged if production has no auth.
func NewNatsBus(url string) (*NatsBus, error) {
	production := env.IsProduction()

	// Enforce TLS in production: reject nats:// unless explicitly allowed.
	if production && !strings.HasPrefix(url, "tls://") {
		if !parseBoolEnv(envNATSAllowPlain) {
			return nil, fmt.Errorf("nats TLS required in production: use tls:// scheme or set %s=true", envNATSAllowPlain)
		}
		slog.Warn("bus: plaintext NATS allowed in production via override", "url", url)
	}

	b := &NatsBus{ackWait: defaultAckWait}
	opts := []nats.Option{
		nats.Name("cordum-bus"),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2 * time.Second),
		nats.PingInterval(natsPingIntervalFromEnv()),
		nats.DisconnectErrHandler(func(nc *nats.Conn, err error) {
			slog.Info("bus: disconnected from nats", "err", err)
			b.runDisconnectHandlers(nc, err)
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			slog.Info("bus: reconnected to nats", "url", nc.ConnectedUrl())
			b.runReconnectHandlers(nc)
		}),
		nats.ClosedHandler(func(nc *nats.Conn) {
			slog.Info("bus: connection closed")
		}),
	}
	if strings.HasPrefix(url, "tls://") {
		if tlsConfig, err := natsTLSConfigFromEnv(); err != nil {
			return nil, fmt.Errorf("nats tls config: %w", err)
		} else if tlsConfig != nil {
			opts = append(opts, nats.Secure(tlsConfig))
		}
	}

	// Authentication: try username/password, then token, then NKey.
	authConfigured := natsApplyAuth(&opts)
	if production && !authConfigured {
		slog.Warn("bus: NATS authentication not configured in production — set NATS_USERNAME/NATS_PASSWORD, NATS_TOKEN, or NATS_NKEY")
	}

	nc, err := nats.Connect(url, opts...)
	if err != nil {
		return nil, fmt.Errorf("connect nats %s: %w", url, err)
	}
	b.nc = nc
	b.initJetStreamFromEnv()
	return b, nil
}

// natsApplyAuth reads auth env vars and appends the appropriate NATS option.
// Returns true if any auth mechanism was configured.
func natsApplyAuth(opts *[]nats.Option) bool {
	username := strings.TrimSpace(os.Getenv(envNATSUsername))
	password := strings.TrimSpace(os.Getenv(envNATSPassword))
	if username != "" && password != "" {
		*opts = append(*opts, nats.UserInfo(username, password))
		return true
	}

	token := strings.TrimSpace(os.Getenv(envNATSToken))
	if token != "" {
		*opts = append(*opts, nats.Token(token))
		return true
	}

	nkey := strings.TrimSpace(os.Getenv(envNATSNKey))
	if nkey != "" {
		opt, err := nats.NkeyOptionFromSeed(nkey)
		if err != nil {
			slog.Error("bus: invalid NATS_NKEY seed", "err", err)
			return false
		}
		*opts = append(*opts, opt)
		return true
	}

	return false
}

// WithRedis sets an optional Redis client for crash-safe message processing.
// When set, durable JetStream subscriptions use Redis-backed idempotency guards
// to prevent duplicate processing after crash/restart. Call before Subscribe.
func (b *NatsBus) WithRedis(client redis.UniversalClient) *NatsBus {
	b.redis = client
	return b
}

// AddReconnectHandler registers a callback invoked from the underlying NATS
// reconnect handler. Handlers run best-effort and must be quick.
func (b *NatsBus) AddReconnectHandler(handler func(*nats.Conn)) {
	if b == nil || handler == nil {
		return
	}
	b.hooksMu.Lock()
	b.reconnectHandlers = append(b.reconnectHandlers, handler)
	b.hooksMu.Unlock()
}

// AddDisconnectHandler registers a callback invoked from the underlying NATS
// disconnect handler. Handlers run best-effort and must be quick.
func (b *NatsBus) AddDisconnectHandler(handler func(*nats.Conn, error)) {
	if b == nil || handler == nil {
		return
	}
	b.hooksMu.Lock()
	b.disconnectHandlers = append(b.disconnectHandlers, handler)
	b.hooksMu.Unlock()
}

func (b *NatsBus) runReconnectHandlers(nc *nats.Conn) {
	if b == nil {
		return
	}
	b.hooksMu.RLock()
	handlers := append([]func(*nats.Conn){}, b.reconnectHandlers...)
	b.hooksMu.RUnlock()
	for _, handler := range handlers {
		func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("bus: reconnect handler panic",
						"panic", r,
						"stack", string(debug.Stack()),
					)
				}
			}()
			handler(nc)
		}()
	}
}

func (b *NatsBus) runDisconnectHandlers(nc *nats.Conn, err error) {
	if b == nil {
		return
	}
	b.hooksMu.RLock()
	handlers := append([]func(*nats.Conn, error){}, b.disconnectHandlers...)
	b.hooksMu.RUnlock()
	for _, handler := range handlers {
		func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("bus: disconnect handler panic",
						"panic", r,
						"stack", string(debug.Stack()),
					)
				}
			}()
			handler(nc, err)
		}()
	}
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
			slog.Warn("bus: drain subscription failed", "subject", sub.Subject, "err", err)
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
	_, err := b.subscribe(subject, queue, handler)
	return err
}

// ReplaceSubscription unsubscribes the previous subscription (when valid) and
// installs a fresh subscription for the same handler. This is primarily used by
// reconnect callbacks that need to force a clean re-subscribe.
func (b *NatsBus) ReplaceSubscription(prev *nats.Subscription, subject, queue string, handler func(*pb.BusPacket) error) (*nats.Subscription, error) {
	if prev != nil && prev.IsValid() {
		if err := prev.Unsubscribe(); err != nil && !errors.Is(err, nats.ErrBadSubscription) {
			return nil, fmt.Errorf("unsubscribe %s: %w", subject, err)
		}
	}
	return b.subscribe(subject, queue, handler)
}

func (b *NatsBus) subscribe(subject, queue string, handler func(*pb.BusPacket) error) (*nats.Subscription, error) {
	if b == nil || b.nc == nil {
		return nil, errNilBus
	}
	if subject == "" {
		return nil, errEmptyTopic
	}
	if handler == nil {
		return nil, errors.New("nil handler")
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
					slog.Warn("bus: terminating poison message", "subject", subject, "deliveries", numDelivered, "stream_seq", meta.Sequence.Stream, "consumer_seq", meta.Sequence.Consumer)
					// DLQ write BEFORE Term — prevents data loss if we crash between Term and DLQ write.
					if b.OnMessageTerminated != nil {
						if dlqErr := b.OnMessageTerminated(subject, msg.Data, numDelivered); dlqErr != nil {
							slog.Error("bus: dlq write failed, nak-ing for retry", "subject", subject, "err", dlqErr)
							_ = msg.NakWithDelay(5 * time.Second)
							return
						}
					}
					if termErr := msg.Term(); termErr != nil {
						slog.Error("bus: term failed", "subject", subject, "err", termErr)
					}
					return
				}
				if numDelivered > 50 {
					slog.Warn("bus: message redelivered many times", "subject", subject, "deliveries", numDelivered, "max", maxJSRedeliveries)
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
					slog.Error("bus: idempotency check failed, nak-ing for retry", "subject", subject, "stream_seq", streamSeq, "err", err)
					_ = msg.NakWithDelay(2 * time.Second)
					return
				} else if exists > 0 {
					// Already processed — just Ack and skip.
					if ackErr := msg.Ack(); ackErr != nil {
						slog.Warn("bus: ack dedup failed", "subject", subject, "stream_seq", streamSeq, "err", ackErr)
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
				slog.Warn("bus: terminating non-retryable message", "subject", subject, "deliveries", numDelivered)
				// DLQ write BEFORE Term — prevents data loss if we crash between Term and DLQ write.
				if b.OnMessageTerminated != nil {
					if dlqErr := b.OnMessageTerminated(subject, msg.Data, numDelivered); dlqErr != nil {
						slog.Error("bus: dlq write failed, nak-ing for retry", "subject", subject, "err", dlqErr)
						_ = msg.NakWithDelay(5 * time.Second)
						break
					}
				}
				if termErr := msg.Term(); termErr != nil {
					slog.Error("bus: term failed", "subject", subject, "err", termErr)
				}
			case msgActionNakDelay:
				slog.Warn("bus: nak-with-delay", "subject", subject, "delay", delay)
				if nakErr := msg.NakWithDelay(delay); nakErr != nil {
					slog.Error("bus: nak-with-delay failed", "subject", subject, "delay", delay, "err", nakErr)
				}
			case msgActionNak:
				if nakErr := msg.Nak(); nakErr != nil {
					slog.Error("bus: nak failed", "subject", subject, "err", nakErr)
				}
			default:
				// Mark as processed BEFORE Ack so redelivery finds the guard.
				if b.redis != nil && streamSeq > 0 {
					pKey := processedKey(streamName, streamSeq)
					rCtx, rCancel := context.WithTimeout(context.Background(), 2*time.Second)
					if setErr := b.redis.Set(rCtx, pKey, "1", processedKeyTTL).Err(); setErr != nil {
						slog.Error("bus: idempotency set failed, nak-ing for retry", "subject", subject, "stream_seq", streamSeq, "err", setErr)
						rCancel()
						_ = msg.NakWithDelay(2 * time.Second)
						return
					}
					rCancel()
				}
				if ackErr := msg.Ack(); ackErr != nil {
					slog.Error("bus: ack failed", "subject", subject, "err", ackErr)
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
			return nil, fmt.Errorf("subscribe %s: %w", subject, err)
		}
		b.trackSub(sub)
		return sub, nil
	}

	cb := func(msg *nats.Msg) {
		var packet pb.BusPacket
		if err := proto.Unmarshal(msg.Data, &packet); err != nil {
			slog.Error("bus: failed to unmarshal packet", "subject", subject, "err", err)
			return
		}
		if err := handler(&packet); err != nil {
			slog.Error("bus: handler error", "subject", subject, "err", err)
		}
	}
	if queue == "" {
		sub, err := b.nc.Subscribe(subject, cb)
		if err != nil {
			return nil, fmt.Errorf("subscribe %s: %w", subject, err)
		}
		b.trackSub(sub)
		return sub, nil
	}
	sub, err := b.nc.QueueSubscribe(subject, queue, cb)
	if err != nil {
		return nil, fmt.Errorf("subscribe %s: %w", subject, err)
	}
	b.trackSub(sub)
	return sub, nil
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

func natsPingIntervalFromEnv() time.Duration {
	if raw := strings.TrimSpace(os.Getenv(envNATSPingInterval)); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			return d
		}
	}
	return defaultNATSPingInterval
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
		slog.Error("bus: jetstream init failed", "err", err)
		return
	}
	if _, err := js.AccountInfo(); err != nil {
		slog.Error("bus: jetstream not available", "err", err)
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
			slog.Info("bus: jetstream stream ensured", "stream", name, "subjects", subjects, "max_age", maxAge) // #nosec G706 -- structured slog, name is from internal config
			return
		}
		// Stream may already exist; treat that as success.
		if _, infoErr := js.StreamInfo(name); infoErr == nil {
			return
		}
		slog.Error("bus: jetstream ensure stream failed", "stream", name, "err", err)
	}
	ensureStream(streamSys, []string{"sys.>"})
	ensureStream(streamJobs, []string{"job.>", "worker.*.jobs"})

	b.js = js
	b.jsEnabled = true
	b.ackWait = ackWait
	slog.Info("bus: jetstream enabled", "ack_wait", ackWait, "replicas", replicas)
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
