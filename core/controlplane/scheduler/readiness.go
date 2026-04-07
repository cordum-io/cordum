package scheduler

import (
	"log/slog"
	"os"
	"strings"
	"time"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const (
	defaultWorkerReadinessTTL          = 60 * time.Second
	handshakeReadyTopicsFieldNumber    = protowire.Number(6)
	workerReadinessRequiredEnvVariable = "WORKER_READINESS_REQUIRED"
	workerReadinessTTLEnvVariable      = "WORKER_READINESS_TTL"
)

func workerReadinessTTLFromEnv() time.Duration {
	raw := strings.TrimSpace(os.Getenv(workerReadinessTTLEnvVariable))
	if raw == "" {
		return defaultWorkerReadinessTTL
	}
	ttl, err := time.ParseDuration(raw)
	if err != nil || ttl <= 0 {
		slog.Warn("invalid worker readiness ttl, using default",
			"env", workerReadinessTTLEnvVariable,
			"value", raw,
			"default", defaultWorkerReadinessTTL,
			"error", err,
		)
		return defaultWorkerReadinessTTL
	}
	return ttl
}

func workerReadinessRequiredFromEnv() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(workerReadinessRequiredEnvVariable))) {
	case "1", "t", "true", "y", "yes", "on":
		return true
	default:
		return false
	}
}

func readyTopicsFromHandshake(hs *pb.Handshake) []string {
	if hs == nil {
		return nil
	}

	if topics := readyTopicsFromKnownFields(hs); len(topics) > 0 {
		return topics
	}
	return readyTopicsFromUnknownFields(hs)
}

func readyTopicsFromKnownFields(hs *pb.Handshake) []string {
	msg := hs.ProtoReflect()
	field := msg.Descriptor().Fields().ByNumber(handshakeReadyTopicsFieldNumber)
	if field == nil || field.Cardinality() != protoreflect.Repeated || field.Kind() != protoreflect.StringKind {
		return nil
	}

	list := msg.Get(field).List()
	if list.Len() == 0 {
		return nil
	}

	topics := make([]string, 0, list.Len())
	for i := 0; i < list.Len(); i++ {
		topics = append(topics, list.Get(i).String())
	}
	return sanitizeReadyTopics(topics)
}

func readyTopicsFromUnknownFields(hs *pb.Handshake) []string {
	raw := hs.ProtoReflect().GetUnknown()
	if len(raw) == 0 {
		return nil
	}

	var topics []string
	for len(raw) > 0 {
		fieldNum, wireType, tagLen := protowire.ConsumeTag(raw)
		if tagLen < 0 {
			return nil
		}
		raw = raw[tagLen:]
		if fieldNum == handshakeReadyTopicsFieldNumber && wireType == protowire.BytesType {
			value, valueLen := protowire.ConsumeBytes(raw)
			if valueLen < 0 {
				return nil
			}
			topics = append(topics, string(value))
			raw = raw[valueLen:]
			continue
		}
		valueLen := protowire.ConsumeFieldValue(fieldNum, wireType, raw)
		if valueLen < 0 {
			return nil
		}
		raw = raw[valueLen:]
	}
	return sanitizeReadyTopics(topics)
}

func sanitizeReadyTopics(topics []string) []string {
	if len(topics) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(topics))
	out := make([]string, 0, len(topics))
	for _, topic := range topics {
		topic = strings.TrimSpace(topic)
		if topic == "" {
			continue
		}
		if _, exists := seen[topic]; exists {
			continue
		}
		seen[topic] = struct{}{}
		out = append(out, topic)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func workerReadyForTopic(state WorkerReadiness, topic string) bool {
	if !state.Ready {
		return false
	}
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return false
	}
	for _, readyTopic := range state.ReadyTopics {
		if strings.TrimSpace(readyTopic) == topic {
			return true
		}
	}
	return false
}
