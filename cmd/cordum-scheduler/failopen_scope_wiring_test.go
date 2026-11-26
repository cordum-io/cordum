package main

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReloadFailModesAppliesInputFailOpenScope(t *testing.T) {
	svc, err := configsvc.New("redis://" + miniredis.RunT(t).Addr())
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, svc.Close()) })
	ctx := context.Background()
	r := scheduler.NewFailModeResolver(svc, time.Hour)
	engine := scheduler.NewEngine(nil, nil, nil, nil, nil, nil).WithFailModeResolver(r)
	for _, next := range []map[string]any{{"scheduler": map[string]any{"input_fail_mode": "open"}}, {}} {
		require.NoError(t, svc.SetWithRetry(ctx, configsvc.ScopeSystem, "default", 3, func(doc *configsvc.Document) error {
			doc.Data = map[string]any{"scheduler": map[string]any{
				"input_fail_mode":       "open",
				"input_fail_open_scope": []any{map[string]any{"rule": "reports", "topics": []any{"job.reports.read"}}},
			}}
			return nil
		}))
		reloadFailModes(ctx, svc, engine, "test")
		rule, _, ok := r.InputFailOpenRule("", "job.reports.read")
		assert.True(t, ok)
		assert.Equal(t, "reports", rule)
		_, _, ok = r.InputFailOpenRule("", "job.default")
		assert.False(t, ok)
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		reloadFailModes(cancelled, svc, engine, "read-error")
		rule, _, ok = r.InputFailOpenRule("", "job.reports.read")
		assert.True(t, ok)
		assert.Equal(t, "reports", rule)
		doc, err := svc.Get(ctx, configsvc.ScopeSystem, "default")
		require.NoError(t, err)
		doc.Data = next
		require.NoError(t, svc.Set(ctx, doc))
		reloadFailModes(ctx, svc, engine, "test-clear")
		_, _, ok = r.InputFailOpenRule("", "job.reports.read")
		assert.False(t, ok)
	}
}

func TestReloadFailModesPublishesModeAndScopeTogether(t *testing.T) {
	svc, err := configsvc.New("redis://" + miniredis.RunT(t).Addr())
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, svc.Close()) })
	ctx := context.Background()
	r := scheduler.NewFailModeResolver(svc, time.Hour)
	engine := scheduler.NewEngine(nil, nil, nil, nil, nil, nil).WithFailModeResolver(r)
	scope := func(rule, topic string) []any {
		return []any{map[string]any{"rule": rule, "topics": []any{topic}}}
	}
	openA := map[string]any{"input_fail_mode": "open", "input_fail_open_scope": scope("rule-a", "job.a")}
	// SetWithRetry keeps existing system keys, so each step replaces the doc with Get+Set.
	require.NoError(t, svc.SetWithRetry(ctx, configsvc.ScopeSystem, "default", 3, func(*configsvc.Document) error { return nil }))
	for _, step := range []struct {
		name     string
		cfg      map[string]any // the system "scheduler" map; nil removes it
		admitted string         // the only topic admitted after the reload
	}{
		{"open A", openA, "job.a"},
		{"closed B", map[string]any{"input_fail_mode": "closed", "input_fail_open_scope": scope("rule-b", "job.b")}, ""},
		{"open A again", openA, "job.a"},
		{"mode key removed", map[string]any{"input_fail_open_scope": scope("rule-b", "job.b")}, ""},
		{"open A once more", openA, "job.a"},
		{"scheduler map removed", nil, ""},
	} {
		doc, err := svc.Get(ctx, configsvc.ScopeSystem, "default")
		require.NoError(t, err)
		doc.Data = map[string]any{}
		if step.cfg != nil {
			doc.Data["scheduler"] = step.cfg
		}
		require.NoError(t, svc.Set(ctx, doc))
		reloadFailModes(ctx, svc, engine, "test")
		for _, topic := range []string{"job.a", "job.b"} {
			rule, _, ok := r.InputFailOpenRule("", topic)
			if topic == step.admitted {
				assert.True(t, ok, "%s: %s must be admitted", step.name, topic)
				assert.Equal(t, "rule-a", rule, step.name)
				continue
			}
			assert.False(t, ok, "%s: %s must not be admitted", step.name, topic)
			assert.Empty(t, rule, step.name)
		}
	}
}

func TestSchedulerBootWiresAuditSinkBeforeStart(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	require.NoError(t, err)
	body := mainFunction(file)
	require.NotNil(t, body)
	positions := map[string]token.Pos{}
	ast.Inspect(body, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok {
			positions[bootCallName(call.Fun)] = call.Pos()
		}
		return true
	})
	ordered := []string{"newHandshakeAuditSink", "engine.WithDispatchAuditSink", "engine.Start"}
	for i, name := range ordered {
		require.NotEqual(t, token.NoPos, positions[name], "missing boot call %s", name)
		if i > 0 {
			assert.Less(t, positions[ordered[i-1]], positions[name], "boot order: %v", ordered)
		}
	}
}
