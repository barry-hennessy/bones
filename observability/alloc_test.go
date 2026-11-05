package observability_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/barry-hennessy/bones/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Allocation guards. testing.AllocsPerRun is deterministic enough to assert
// on; the ceilings are the measured values, not slack.
func TestAllocations(t *testing.T) {
	ctx := context.Background()

	startEnd := func(lt observability.LoggingTracer) func() {
		return func() {
			_, _, span := lt.Start(ctx, "op")
			span.End()
		}
	}

	t.Run("zero value Start+End", func(t *testing.T) {
		assert.Zero(t, testing.AllocsPerRun(100, startEnd(observability.LoggingTracer{})))
	})

	t.Run("named, no tracer Start+End", func(t *testing.T) {
		lt := observability.LoggingTracer{}.Component("orders")
		assert.Zero(t, testing.AllocsPerRun(100, startEnd(lt)))
	})

	t.Run("named with custom Log, no tracer Start+End", func(t *testing.T) {
		lt := observability.LoggingTracer{Log: *slog.New(slog.NewJSONHandler(io.Discard, nil))}.Component("orders")
		assert.Zero(t, testing.AllocsPerRun(100, startEnd(lt)))
	})

	t.Run("named Logger", func(t *testing.T) {
		lt := observability.LoggingTracer{}.Component("orders")
		assert.Zero(t, testing.AllocsPerRun(100, func() { _ = lt.Logger() }))
	})

	t.Run("Name with the current or an empty name", func(t *testing.T) {
		lt := observability.LoggingTracer{}.Component("orders")

		var sink observability.LoggingTracer

		assert.Zero(t, testing.AllocsPerRun(100, func() { sink = lt.Component("orders") }))
		assert.Zero(t, testing.AllocsPerRun(100, func() { sink = lt.Component("") }))
		_ = sink
	})

	t.Run("Name(same).Start on a pre-named tracer", func(t *testing.T) {
		lt := observability.LoggingTracer{}.Component("orders")
		assert.Zero(t, testing.AllocsPerRun(100, func() {
			_, _, span := lt.Component("orders").Start(ctx, "orders")
			span.End()
		}))
	})
}

// The named logger is cached by Name; it must still follow its inputs.
func TestNamedLoggerCacheIsNotStale(t *testing.T) {
	line := func(t *testing.T, b *bytes.Buffer) map[string]any {
		t.Helper()

		var m map[string]any
		require.NoError(t, json.Unmarshal(bytes.TrimSpace(b.Bytes()), &m))
		b.Reset()

		return m
	}

	t.Run("slog.SetDefault after Name", func(t *testing.T) {
		old := slog.Default()
		t.Cleanup(func() { slog.SetDefault(old) })

		lt := observability.LoggingTracer{}.Component("orders")

		var buf bytes.Buffer
		slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))

		lt.Logger().Info("after set default")
		assert.Equal(t, "orders", line(t, &buf)["component"])

		_, log, span := lt.Start(t.Context(), "op")
		span.End()
		log.Info("from start")
		assert.Equal(t, "orders", line(t, &buf)["component"])
	})

	t.Run("Log reassigned after Name", func(t *testing.T) {
		var first, second bytes.Buffer

		lt := observability.LoggingTracer{Log: *slog.New(slog.NewJSONHandler(&first, nil))}.Component("orders")
		lt.Log = *slog.New(slog.NewJSONHandler(&second, nil))

		lt.Logger().Info("hello")

		assert.Empty(t, first.String())
		assert.Equal(t, "orders", line(t, &second)["component"])
	})

	t.Run("renaming does not leak the old component", func(t *testing.T) {
		var buf bytes.Buffer

		lt := observability.LoggingTracer{Log: *slog.New(slog.NewJSONHandler(&buf, nil))}.Component("orders")
		lt.Component("billing").Logger().Info("x")

		assert.Equal(t, "billing", line(t, &buf)["component"])

		lt.Logger().Info("y")
		assert.Equal(t, "orders", line(t, &buf)["component"])
	})
}
