package tracescan

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/redis/go-redis/v9"
)

// Find searches key names and values in the supplied client's keyspace, returning
// key or key:<field|member|index|id> locations. Absence only holds for the client
// used by the writers: a cleanup proof must first require a hit before cleanup.
// Call on quiescent proof fixtures; SCAN is not a snapshot of concurrent writes.
func Find(ctx context.Context, client redis.UniversalClient, marker string) ([]string, error) {
	if marker == "" {
		return nil, errors.New("tracescan: empty marker")
	}
	var mu sync.Mutex
	var hits []string
	scan := func(ctx context.Context, node *redis.Client) error {
		if node == nil {
			return errors.New("tracescan: nil client")
		}
		found, err := scanNode(ctx, node, marker)
		mu.Lock()
		defer mu.Unlock()
		hits = append(hits, found...)
		return err
	}
	var err error
	switch c := client.(type) {
	case *redis.Client:
		err = scan(ctx, c)
	case *redis.ClusterClient:
		if c == nil {
			return nil, errors.New("tracescan: nil cluster client")
		}
		err = c.ForEachMaster(ctx, scan)
	default:
		return nil, fmt.Errorf("tracescan: unsupported client %T", client)
	}
	if err != nil {
		return nil, fmt.Errorf("tracescan: %w", err)
	}
	slices.Sort(hits)
	return slices.Compact(hits), nil
}

func scanNode(ctx context.Context, client redis.Cmdable, marker string) ([]string, error) {
	var hits []string
	keys := client.Scan(ctx, 0, "", 128).Iterator()
	for keys.Next(ctx) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		key := keys.Val()
		found, err := scanKey(ctx, client, key, marker)
		if errors.Is(err, redis.Nil) {
			continue
		}
		if err != nil {
			return nil, err
		}
		hits = append(hits, found...)
	}
	return hits, keys.Err()
}

func scanKey(ctx context.Context, client redis.Cmdable, key, marker string) ([]string, error) {
	kind, err := client.Type(ctx, key).Result()
	if err != nil || kind == "none" {
		return nil, err
	}
	var hits []string
	match := func(location, value string) {
		if strings.Contains(value, marker) {
			hits = append(hits, location)
		}
	}
	match(key, key)
	switch kind {
	case "string":
		var value string
		value, err = client.Get(ctx, key).Result()
		match(key, value)
	case "hash", "set", "zset":
		err = scanMembers(ctx, client, key, kind, match)
	case "list":
		err = scanList(ctx, client, key, match)
	case "stream":
		err = scanStream(ctx, client, key, match)
	default:
		err = fmt.Errorf("unsupported Redis type %q", kind)
	}
	return hits, err
}

func scanMembers(ctx context.Context, client redis.Cmdable, key, kind string, match func(string, string)) error {
	var cursor uint64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var cmd *redis.ScanCmd
		switch kind {
		case "hash":
			cmd = client.HScan(ctx, key, cursor, "", 128)
		case "set":
			cmd = client.SScan(ctx, key, cursor, "", 128)
		case "zset":
			cmd = client.ZScan(ctx, key, cursor, "", 128)
		}
		values, next, err := cmd.Result()
		if err != nil {
			return err
		}
		for i := 0; i < len(values); i++ {
			location := key + ":" + values[i]
			match(location, values[i])
			if kind != "set" {
				i++
				if kind == "hash" {
					match(location, values[i])
				}
			}
		}
		cursor = next
		if cursor == 0 {
			return nil
		}
	}
}

func scanList(ctx context.Context, client redis.Cmdable, key string, match func(string, string)) error {
	for start := int64(0); ; start += 128 {
		if err := ctx.Err(); err != nil {
			return err
		}
		values, err := client.LRange(ctx, key, start, start+127).Result()
		if err != nil {
			return err
		}
		for i, value := range values {
			match(key+":"+strconv.FormatInt(start+int64(i), 10), value)
		}
		if len(values) < 128 {
			return nil
		}
	}
}

func scanStream(ctx context.Context, client redis.Cmdable, key string, match func(string, string)) error {
	for start := "-"; ; {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries, err := client.XRangeN(ctx, key, start, "+", 128).Result()
		if err != nil {
			return err
		}
		for _, entry := range entries {
			for field, value := range entry.Values {
				match(key+":"+entry.ID, field)
				match(key+":"+entry.ID, fmt.Sprint(value))
			}
		}
		if len(entries) < 128 {
			return nil
		}
		start = "(" + entries[len(entries)-1].ID
	}
}
