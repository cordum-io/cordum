package testredis

import (
	"context"
	"fmt"
	"strconv"

	"github.com/redis/go-redis/v9"
)

type crossSlotGuard struct{}

// CrossSlotGuard rejects multi-key commands before sending them, including in
// pipelines. It is stricter than a real cluster: one key per command is one slot,
// even when distinct keys share a hash tag. It covers the multi-key audit's commands.
func CrossSlotGuard() redis.Hook                                   { return crossSlotGuard{} }
func (crossSlotGuard) DialHook(next redis.DialHook) redis.DialHook { return next }
func (crossSlotGuard) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if crossSlotCommand(cmd) {
			return redis.ErrCrossSlot
		}
		return next(ctx, cmd)
	}
}
func (crossSlotGuard) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		for _, cmd := range cmds {
			if crossSlotCommand(cmd) {
				return redis.ErrCrossSlot
			}
		}
		return next(ctx, cmds)
	}
}

func crossSlotCommand(cmd redis.Cmder) bool {
	args := cmd.Args()
	start, end, stride := 1, len(args), 1
	countAt, destination := 0, false
	switch cmd.Name() {
	case "del", "unlink", "exists", "touch", "mget", "sinter", "sunion", "sdiff",
		"sinterstore", "sunionstore", "sdiffstore", "pfcount", "pfmerge":
	case "mset", "msetnx":
		stride = 2
	case "rename", "renamenx", "smove", "lmove", "blmove", "rpoplpush", "brpoplpush", "copy":
		end = min(end, 3)
	case "zinterstore", "zunionstore", "zdiffstore":
		countAt, destination = 2, true
	case "zinter", "zunion", "zdiff", "zintercard", "sintercard":
		countAt = 1
	default:
		return false
	}
	if countAt != 0 {
		if len(args) <= countAt {
			return false
		}
		n, err := strconv.Atoi(fmt.Sprint(args[countAt]))
		start = countAt + 1
		if err != nil || n <= 0 || n > len(args)-start {
			return false
		}
		end = start + n
	}
	if start >= end {
		return false
	}
	key := fmt.Sprint(args[start])
	if destination && fmt.Sprint(args[1]) != key {
		return true
	}
	for i := start + stride; i < end; i += stride {
		if fmt.Sprint(args[i]) != key {
			return true
		}
	}
	return false
}
