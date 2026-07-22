package gateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/cordum/cordum/core/infra/store"
	"github.com/redis/go-redis/v9"
)

const legacyMemoryTooLargeMarker = "CORDUM_MEMORY_TOO_LARGE"

var (
	boundedRedisStringScript = redis.NewScript(
		"local value = redis.call('GET', KEYS[1])\n" +
			"if not value then return redis.error_reply('CORDUM_MEMORY_MISSING') end\n" +
			"if string.len(value) > tonumber(ARGV[1]) then\n" +
			"  return redis.error_reply('CORDUM_MEMORY_TOO_LARGE')\n" +
			"end\n" +
			"return value",
	)
	boundedRedisListScript = redis.NewScript(
		"if redis.call('EXISTS', KEYS[1]) == 0 then return redis.error_reply('CORDUM_MEMORY_MISSING') end\n" +
			"if redis.call('LLEN', KEYS[1]) > tonumber(ARGV[1]) then return redis.error_reply('CORDUM_MEMORY_TOO_LARGE') end\n" +
			"local values = redis.call('LRANGE', KEYS[1], 0, -1)\n" +
			"local total = 0\n" +
			"for _, value in ipairs(values) do total = total + string.len(value) end\n" +
			"if total > tonumber(ARGV[2]) then return redis.error_reply('CORDUM_MEMORY_TOO_LARGE') end\n" +
			"return values",
	)
	boundedRedisSetScript = redis.NewScript(
		"if redis.call('EXISTS', KEYS[1]) == 0 then return redis.error_reply('CORDUM_MEMORY_MISSING') end\n" +
			"if redis.call('SCARD', KEYS[1]) > tonumber(ARGV[1]) then return redis.error_reply('CORDUM_MEMORY_TOO_LARGE') end\n" +
			"local values = redis.call('SMEMBERS', KEYS[1])\n" +
			"local total = 0\n" +
			"for _, value in ipairs(values) do total = total + string.len(value) end\n" +
			"if total > tonumber(ARGV[2]) then return redis.error_reply('CORDUM_MEMORY_TOO_LARGE') end\n" +
			"return values",
	)
	boundedRedisHashScript = redis.NewScript(
		"if redis.call('EXISTS', KEYS[1]) == 0 then return redis.error_reply('CORDUM_MEMORY_MISSING') end\n" +
			"if redis.call('HLEN', KEYS[1]) > tonumber(ARGV[1]) then return redis.error_reply('CORDUM_MEMORY_TOO_LARGE') end\n" +
			"local values = redis.call('HGETALL', KEYS[1])\n" +
			"local total = 0\n" +
			"for _, value in ipairs(values) do total = total + string.len(value) end\n" +
			"if total > tonumber(ARGV[2]) then return redis.error_reply('CORDUM_MEMORY_TOO_LARGE') end\n" +
			"return values",
	)
)

func (s *server) loadRedisMemory(ctx context.Context, key string) ([]byte, error) {
	redisStore, ok := s.memStore.(*store.RedisStore)
	if !ok || redisStore == nil || redisStore.Client() == nil {
		return nil, errMemoryInspectionUnavailable
	}
	client := redisStore.Client()
	redisType, err := client.Type(ctx, key).Result()
	if err != nil {
		return nil, err
	}
	switch redisType {
	case "none":
		return nil, redis.Nil
	case "string":
		return encodeRedisString(ctx, client, key)
	case "list":
		return encodeRedisList(ctx, client, key)
	case "set":
		return encodeRedisSet(ctx, client, key)
	case "hash":
		return encodeRedisHash(ctx, client, key)
	default:
		return nil, errUnsupportedMemoryType
	}
}

func encodeRedisString(ctx context.Context, client redis.UniversalClient, key string) ([]byte, error) {
	text, err := boundedRedisStringScript.Run(
		ctx, client, []string{key}, maxResolvedMemoryBytes,
	).Text()
	if err != nil {
		return nil, normalizeLegacyRedisError(err)
	}
	raw := []byte(text)
	value := any(base64.StdEncoding.EncodeToString(raw))
	if utf8.Valid(raw) {
		value = decodeRedisJSONValue(string(raw))
	}
	return marshalBoundedLegacyMemory(map[string]any{"redis_type": "string", "value": value})
}

func encodeRedisList(ctx context.Context, client redis.UniversalClient, key string) ([]byte, error) {
	items, err := boundedRedisValues(ctx, client, boundedRedisListScript, key)
	if err != nil {
		return nil, err
	}
	decoded := make([]any, 0, len(items))
	for _, item := range items {
		decoded = append(decoded, decodeRedisJSONValue(item))
	}
	return marshalBoundedLegacyMemory(map[string]any{
		"redis_type": "list", "length": len(decoded), "items": decoded,
	})
}

func encodeRedisSet(ctx context.Context, client redis.UniversalClient, key string) ([]byte, error) {
	items, err := boundedRedisValues(ctx, client, boundedRedisSetScript, key)
	if err != nil {
		return nil, err
	}
	decoded := make([]any, 0, len(items))
	for _, item := range items {
		decoded = append(decoded, decodeRedisJSONValue(item))
	}
	return marshalBoundedLegacyMemory(map[string]any{
		"redis_type": "set", "length": len(decoded), "items": decoded,
	})
}

func encodeRedisHash(ctx context.Context, client redis.UniversalClient, key string) ([]byte, error) {
	values, err := boundedRedisValues(ctx, client, boundedRedisHashScript, key)
	if err != nil {
		return nil, err
	}
	if len(values)%2 != 0 {
		return nil, errMemoryInspectionUnavailable
	}
	decoded := make(map[string]any, len(values)/2)
	for index := 0; index < len(values); index += 2 {
		decoded[values[index]] = decodeRedisJSONValue(values[index+1])
	}
	return marshalBoundedLegacyMemory(map[string]any{
		"redis_type": "hash", "length": len(decoded), "items": decoded,
	})
}

func boundedRedisValues(
	ctx context.Context,
	client redis.UniversalClient,
	script *redis.Script,
	key string,
) ([]string, error) {
	values, err := script.Run(
		ctx, client, []string{key}, maxLegacyMemoryEntries, maxResolvedMemoryBytes,
	).StringSlice()
	if err != nil {
		return nil, normalizeLegacyRedisError(err)
	}
	if len(values) > maxLegacyMemoryEntries*2 {
		return nil, errMemoryResourceTooLarge
	}
	return values, nil
}

func marshalBoundedLegacyMemory(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(encoded) > maxResolvedMemoryBytes {
		return nil, errMemoryResourceTooLarge
	}
	return encoded, nil
}

func normalizeLegacyRedisError(err error) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	if strings.Contains(message, legacyMemoryTooLargeMarker) {
		return errMemoryResourceTooLarge
	}
	if strings.Contains(message, "CORDUM_MEMORY_MISSING") {
		return redis.Nil
	}
	return err
}

func decodeRedisJSONValue(value string) any {
	if strings.TrimSpace(value) == "" {
		return value
	}
	var decoded any
	if json.Unmarshal([]byte(value), &decoded) == nil {
		return decoded
	}
	return value
}

func writeLegacyMemoryError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, redis.Nil):
		writeErrorJSON(w, http.StatusNotFound, "not found")
	case errors.Is(err, errMemoryResourceTooLarge):
		writeErrorJSON(w, http.StatusRequestEntityTooLarge, "memory resource too large")
	case errors.Is(err, errMemoryInspectionUnavailable):
		writeErrorJSON(w, http.StatusServiceUnavailable, "memory inspection unavailable")
	case errors.Is(err, errUnsupportedMemoryType):
		writeErrorJSON(w, http.StatusBadRequest, "unsupported memory type")
	default:
		writeErrorJSON(w, http.StatusInternalServerError, "internal error")
	}
}

func writeLegacyMemoryResponse(w http.ResponseWriter, data []byte, kind string) {
	if len(data) > maxResolvedMemoryBytes {
		writeLegacyMemoryError(w, errMemoryResourceTooLarge)
		return
	}
	response := map[string]any{
		"kind":       kind,
		"size_bytes": len(data),
		"base64":     base64.StdEncoding.EncodeToString(data),
	}
	if utf8.Valid(data) {
		response["text"] = string(data)
	}
	var decoded any
	if json.Unmarshal(data, &decoded) == nil {
		response["json"] = decoded
	}
	writeJSON(w, response)
}
