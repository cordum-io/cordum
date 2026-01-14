package bus

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
)

// NatsBus is a thin wrapper over a NATS connection that speaks protobuf packets.
type NatsBus struct {
	nc        *nats.Conn
	js        nats.JetStreamContext
	jsEnabled bool
	ackWait   time.Duration
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
)

var (
	errNilBus     = errors.New("nats bus not initialized")
	errNilPacket  = errors.New("nil bus packet")
	errEmptyTopic = errors.New("empty subject")
)

// NewNatsBus dials NATS at the provided URL.
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
	if tlsConfig, err := natsTLSConfigFromEnv(); err != nil {
		return nil, err
	} else if tlsConfig != nil {
		opts = append(opts, nats.Secure(tlsConfig))
	}

	nc, err := nats.Connect(url, opts...)
	if err != nil {
		return nil, err
	}
	b := &NatsBus{nc: nc, ackWait: defaultAckWait}
	b.initJetStreamFromEnv()
	return b, nil
}

// Close shuts down the underlying NATS connection.
func (b *NatsBus) Close() {
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
		return err
	}
	if b != nil && b.jsEnabled && isDurableSubject(subject) {
		msgID := computeMsgID(subject, packet)
		if msgID != "" {
			_, err = b.js.Publish(subject, data, nats.MsgId(msgID))
		} else {
			_, err = b.js.Publish(subject, data)
		}
		return err
	}
	return b.nc.Publish(subject, data)
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
			var packet pb.BusPacket
			if err := proto.Unmarshal(msg.Data, &packet); err != nil {
				log.Printf("nats bus: failed to unmarshal packet: %v", err)
				_ = msg.Ack()
				return
			}
			if err := handler(&packet); err != nil {
				if delay, ok := RetryDelay(err); ok {
					if delay > 0 {
						_ = msg.NakWithDelay(delay)
					} else {
						_ = msg.Nak()
					}
					return
				}
				log.Printf("nats bus: handler error (ack): %v", err)
				_ = msg.Ack()
				return
			}
			_ = msg.Ack()
		}

		opts := []nats.SubOpt{
			nats.ManualAck(),
			nats.AckExplicit(),
			nats.AckWait(b.ackWait),
			nats.MaxAckPending(2048),
		}
		if durable := durableName(subject, queue); durable != "" {
			opts = append(opts, nats.Durable(durable))
		}

		var err error
		if queue == "" {
			_, err = b.js.Subscribe(subject, cb, opts...)
		} else {
			_, err = b.js.QueueSubscribe(subject, queue, cb, opts...)
		}
		return err
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
		_, err := b.nc.Subscribe(subject, cb)
		return err
	}
	_, err := b.nc.QueueSubscribe(subject, queue, cb)
	return err
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

	if caPath == "" && certPath == "" && keyPath == "" && serverName == "" && !insecure {
		return nil, nil
	}

	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if serverName != "" {
		cfg.ServerName = serverName
	}
	if insecure {
		cfg.InsecureSkipVerify = true
	}
	if caPath != "" {
		// #nosec G304 -- CA path is configured by the operator.
		pem, err := os.ReadFile(caPath)
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
	log.Printf("[BUS] jetstream enabled ack_wait=%s replicas=%d", ackWait, replicas)
}

func isDurableSubject(subject string) bool {
	switch subject {
	case capsdk.SubjectSubmit, capsdk.SubjectResult, capsdk.SubjectDLQ:
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
		return "dur_" + name
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
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	return prefix + id
}
