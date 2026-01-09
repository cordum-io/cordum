package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

const (
	defaultMaxHistory     = 20
	defaultMaxInputTokens = 8000
)

// Service implements the ContextEngine RPC service.
type Service struct {
	pb.UnimplementedContextEngineServer
	redis      *redis.Client
	maxHistory int64
}

// NewService constructs a context engine backed by Redis.
func NewService(redisURL string) (*Service, error) {
	if redisURL == "" {
		redisURL = "redis://localhost:6379"
	}
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	client := redis.NewClient(opts)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("connect redis: %w", err)
	}
	return &Service{
		redis:      client,
		maxHistory: defaultMaxHistory,
	}, nil
}

type historyEvent struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	Timestamp int64  `json:"ts"`
}

// BuildWindow assembles model-ready messages from logical payload and stored memory.
func (s *Service) BuildWindow(ctx context.Context, req *pb.BuildWindowRequest) (*pb.BuildWindowResponse, error) {
	memoryID := req.GetMemoryId()
	mode := req.GetMode()
	if mode == pb.ContextMode_CONTEXT_MODE_UNSPECIFIED {
		mode = pb.ContextMode_CONTEXT_MODE_RAW
	}

	userMsg := s.extractUserMessage(req.GetLogicalPayload())
	messages := []*pb.ModelMessage{}

	// Pull recent history for CHAT/RAG.
	if memoryID != "" && (mode == pb.ContextMode_CONTEXT_MODE_CHAT || mode == pb.ContextMode_CONTEXT_MODE_RAG) {
		events, _ := s.redis.LRange(ctx, s.historyKey(memoryID), -s.maxHistory, -1).Result()
		for _, raw := range events {
			var ev historyEvent
			if err := json.Unmarshal([]byte(raw), &ev); err == nil && ev.Content != "" {
				messages = append(messages, &pb.ModelMessage{Role: ev.Role, Content: ev.Content})
			}
		}
	}

	// RAG: attach stored chunks that match file_path when present.
	if memoryID != "" && mode == pb.ContextMode_CONTEXT_MODE_RAG {
		filePath := s.extractFilePath(req.GetLogicalPayload())
		if filePath == "" {
			if summary := s.loadSummary(ctx, memoryID); summary != "" {
				messages = append(messages, &pb.ModelMessage{
					Role:    "system",
					Content: summary,
				})
			}
		} else {
			chunks := s.loadChunks(ctx, memoryID)
			for _, ch := range chunks {
				if strings.Contains(ch.Path, filePath) || strings.Contains(filePath, ch.Path) {
					content := ch.Content
					if content == "" {
						content = fmt.Sprintf("File metadata for %s (language=%s, bytes=%d, loc=%d)", ch.Path, ch.Language, ch.Bytes, ch.Loc)
					}
					messages = append(messages, &pb.ModelMessage{
						Role:    "system",
						Content: fmt.Sprintf("Context from %s:\n%s", ch.Path, content),
					})
				}
			}
		}
	}

	// Append current user request last.
	if userMsg != "" {
		messages = append(messages, &pb.ModelMessage{Role: "user", Content: userMsg})
	}

	// Enforce token budget best-effort.
	maxInput := req.GetMaxInputTokens()
	if maxInput == 0 {
		maxInput = defaultMaxInputTokens
	}
	messages = trimToBudget(messages, maxInput)
	inputTokens := estimateTokens(messages)

	outTokens := req.GetMaxOutputTokens()
	if outTokens == 0 {
		outTokens = 1024
	}

	return &pb.BuildWindowResponse{
		Messages:     messages,
		InputTokens:  int32(inputTokens),
		OutputTokens: outTokens,
	}, nil
}

// UpdateMemory appends user/assistant exchanges to history for chat/RAG modes.
func (s *Service) UpdateMemory(ctx context.Context, req *pb.UpdateMemoryRequest) (*pb.UpdateMemoryResponse, error) {
	memoryID := req.GetMemoryId()
	if memoryID == "" {
		return &pb.UpdateMemoryResponse{}, nil
	}
	mode := req.GetMode()
	if mode == pb.ContextMode_CONTEXT_MODE_UNSPECIFIED {
		mode = pb.ContextMode_CONTEXT_MODE_RAW
	}
	if mode == pb.ContextMode_CONTEXT_MODE_RAW {
		return &pb.UpdateMemoryResponse{}, nil
	}

	userMsg := s.extractUserMessage(req.GetLogicalPayload())
	assistantMsg := strings.TrimSpace(string(req.GetModelResponse()))

	pipe := s.redis.Pipeline()
	pushed := false
	if userMsg != "" {
		ev := historyEvent{Role: "user", Content: userMsg, Timestamp: time.Now().Unix()}
		if data, err := json.Marshal(ev); err == nil {
			pipe.RPush(ctx, s.historyKey(memoryID), data)
			pushed = true
		}
	}
	if assistantMsg != "" {
		ev := historyEvent{Role: "assistant", Content: assistantMsg, Timestamp: time.Now().Unix()}
		if data, err := json.Marshal(ev); err == nil {
			pipe.RPush(ctx, s.historyKey(memoryID), data)
			pushed = true
		}
	}
	if pushed && s.maxHistory > 0 {
		pipe.LTrim(ctx, s.historyKey(memoryID), -s.maxHistory, -1)
	}
	_, _ = pipe.Exec(ctx)
	return &pb.UpdateMemoryResponse{}, nil
}

func (s *Service) historyKey(memoryID string) string {
	return fmt.Sprintf("mem:%s:events", memoryID)
}

func (s *Service) chunkIndexKey(memoryID string) string {
	return fmt.Sprintf("mem:%s:chunks", memoryID)
}

func (s *Service) chunkKey(memoryID string, idx int) string {
	return fmt.Sprintf("mem:%s:chunk:%d", memoryID, idx)
}

func (s *Service) summaryKey(memoryID string) string {
	return fmt.Sprintf("mem:%s:summary", memoryID)
}

type chunkRecord struct {
	Path     string `json:"path"`
	Language string `json:"language"`
	Bytes    int64  `json:"bytes"`
	Loc      int64  `json:"loc"`
	Content  string `json:"content"`
}

func (s *Service) loadChunks(ctx context.Context, memoryID string) []chunkRecord {
	if memoryID == "" {
		return nil
	}
	keys, err := s.redis.SMembers(ctx, s.chunkIndexKey(memoryID)).Result()
	if err != nil || len(keys) == 0 {
		return nil
	}
	var out []chunkRecord
	for _, k := range keys {
		val, err := s.redis.Get(ctx, k).Bytes()
		if err != nil {
			continue
		}
		var rec chunkRecord
		if err := json.Unmarshal(val, &rec); err == nil {
			out = append(out, rec)
		}
	}
	return out
}

func (s *Service) loadSummary(ctx context.Context, memoryID string) string {
	if memoryID == "" {
		return ""
	}
	val, err := s.redis.Get(ctx, s.summaryKey(memoryID)).Result()
	if err != nil {
		return ""
	}
	return val
}

func (s *Service) extractUserMessage(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return string(payload)
	}
	if v, ok := raw["prompt"].(string); ok && v != "" {
		return v
	}
	if v, ok := raw["message"].(string); ok && v != "" {
		return v
	}
	if instr, ok := raw["instruction"].(string); ok && instr != "" {
		code := ""
		if c, ok := raw["code_snippet"].(string); ok {
			code = c
		}
		if code != "" {
			return fmt.Sprintf("Instruction: %s\nCode:\n%s", instr, code)
		}
		return instr
	}
	return string(payload)
}

func (s *Service) extractFilePath(payload []byte) string {
	var raw map[string]any
	if len(payload) == 0 {
		return ""
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return ""
	}
	if v, ok := raw["file_path"].(string); ok {
		return v
	}
	return ""
}

func estimateTokens(msgs []*pb.ModelMessage) int {
	total := 0
	for _, m := range msgs {
		total += len(m.GetContent()) / 4
	}
	return total
}

func trimToBudget(msgs []*pb.ModelMessage, maxTokens int32) []*pb.ModelMessage {
	if maxTokens <= 0 {
		return msgs
	}
	for estimateTokens(msgs) > int(maxTokens) && len(msgs) > 1 {
		// drop oldest non-final message
		msgs = msgs[1:]
	}
	return msgs
}
