// Package fixture feeds TestDataInventoryKeyCoverageFixture; it is parsed, never built.
package fixture

import "fmt"

const coveredPrefix = "fix:covered:"

var unknownKey = fmt.Sprintf("fix:unknown:%s", "id")

const labelPrefix = "fix:label:"

const streamName = "FIX_EVENTS"

var appendScript = `redis.call('XADD', KEYS[1], '*', 'event', ARGV[1])`

func ensure(js interface{ AddStream(*nats.StreamConfig) }) {
	add := func(name string) { js.AddStream(&nats.StreamConfig{Name: name}) }
	add(streamName)
	js.AddStream(&nats.StreamConfig{Name: "FIX_DIRECT"})
}
