package observability_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/barry-hennessy/bones/observability"
	"github.com/stretchr/testify/assert"
)

// Allocation guards. testing.AllocsPerRun is deterministic enough to assert
// on; the ceilings are the measured values, not slack.
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

	t.Run("zero value Logger", func(t *testing.T) {
		lt := observability.LoggingTracer{}
		assert.Zero(t, testing.AllocsPerRun(100, func() { _ = lt.Logger() }))
	})

	// An explicit Log is a value field, so handing out a *slog.Logger copies
	// it to the heap once.
	t.Run("custom Log Start+End", func(t *testing.T) {
		lt := observability.LoggingTracer{Log: *slog.New(slog.NewJSONHandler(io.Discard, nil))}
		assert.LessOrEqual(t, testing.AllocsPerRun(100, startEnd(lt)), 1.0)
	})
}
