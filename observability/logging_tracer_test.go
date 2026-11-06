package observability_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/barry-hennessy/bones/observability"
	"github.com/barry-hennessy/bones/observability/mocks"
	"github.com/barry-hennessy/test/sweet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoggingTracer(t *testing.T) {
	sweet.Run(t, "zero value does not panic", zeroLT, func(t *testing.T, lt observability.LoggingTracer) {
		assert.NotPanics(t, func() {
			lt.Logger().Info("zero logger")
		})

		ctx, log, span := lt.Start(t.Context(), "op")
		span.End()
		assert.NotPanics(t, func() {
			log.Info("zero start")
		})
		assert.Equal(t, t.Context(), ctx)
		require.NotNil(t, span)
		assert.False(t, span.IsValid())
		assert.Empty(t, span.TraceID())
		assert.Empty(t, span.SpanID())
	})

	sweet.Run(t, "nil ctx is Background", zeroLT, func(t *testing.T, lt observability.LoggingTracer) {
		assert.NotPanics(t, func() {
			ctx, log, span := lt.Start(nil, "op")
			defer span.End()
			assert.Equal(t, context.Background(), ctx)
			log.Info("nil ctx")
		})
	})

	sweet.Run(t, "nil ctx passed to tracer", recordingLT, func(t *testing.T, d recDeps) {
		_, _, span := d.lt.Start(nil, "op")
		span.End()

		starts := d.tracer.StartCalls()
		require.Len(t, starts, 1)
		assert.Equal(t, context.Background(), starts[0].Ctx)
	})

	sweet.Run(t, "zero Log uses slog.Default", restoreDefault, func(t *testing.T, d jsonDeps) {
		slog.SetDefault(slog.New(slog.NewJSONHandler(d.buf, nil)))

		d.lt.Logger().Info("from default")

		got := logLine(t, d.buf.Bytes())
		assert.Equal(t, "from default", got["msg"])
	})

	sweet.Run(t, "reads slog.Default on each call", restoreDefault, func(t *testing.T, d jsonDeps) {
		lt := d.lt

		slog.SetDefault(slog.New(slog.NewJSONHandler(d.buf, nil)))

		lt.Logger().Info("after setdefault")

		got := logLine(t, d.buf.Bytes())
		assert.Equal(t, "after setdefault", got["msg"])
	})

	sweet.Run(t, "nil tracer skips tracing", jsonLT, func(t *testing.T, d jsonDeps) {
		lt := d.lt
		ctx, spanLog, span := lt.Start(t.Context(), "op")
		defer span.End()

		spanLog.Info("no tracer")

		got := logLine(t, d.buf.Bytes())
		assert.NotContains(t, got, "trace_id")
		assert.NotContains(t, got, "span_id")
		assert.Equal(t, t.Context(), ctx)
		require.NotNil(t, span)
		assert.False(t, span.IsValid())
		assert.Empty(t, span.TraceID())
		assert.Empty(t, span.SpanID())
		assert.False(t, span.IsRecording())
		assert.NotPanics(t, func() {
			span.SetAttribute("k", "v")
			span.AddLink(observability.Link{TraceID: "nope", SpanID: "nope"})
			span.End()
		})
	})

	sweet.Run(t, "nil tracer StartWith NewRoot does not panic", jsonLT, func(t *testing.T, d jsonDeps) {
		ctx, _, span := d.lt.StartWith(t.Context(), "op", observability.StartConfig{NewRoot: true})
		defer span.End()

		assert.Equal(t, t.Context(), ctx)
		require.NotNil(t, span)
		assert.False(t, span.IsValid())
	})

	sweet.Run(t, "Start tags logger and span", recordingLT, func(t *testing.T, d recDeps) {
		_, spanLog, span := d.lt.Start(t.Context(), "orders")
		spanLog.Info("creating")
		span.End()

		got := logLine(t, d.buf.Bytes())
		assert.Equal(t, "creating", got["msg"])
		assert.Equal(t, "trace-1", got["trace_id"])
		assert.Equal(t, "span-1", got["span_id"])

		starts := d.tracer.StartCalls()
		require.Len(t, starts, 1)
		assert.Equal(t, "orders", starts[0].Name)
		assert.Equal(t, observability.StartConfig{}, starts[0].Cfg)

		assert.Empty(t, d.span.SetAttributeCalls())
		require.Len(t, d.span.EndCalls(), 1)
	})

	sweet.Run(t, "Start returns the tracer ctx", recordingLT, func(t *testing.T, d recDeps) {
		ctx, _, span := d.lt.Start(t.Context(), "orders")
		span.End()

		assert.Equal(t, t.Context(), ctx)
	})

	sweet.Run(t, "name is the span name", recordingLT, func(t *testing.T, d recDeps) {
		_, _, span := d.lt.Start(t.Context(), "http.POST /orders")
		span.End()

		starts := d.tracer.StartCalls()
		require.Len(t, starts, 1)
		assert.Equal(t, "http.POST /orders", starts[0].Name)
	})

	sweet.Run(t, "invalid span omits ids", recordingLT, func(t *testing.T, d recDeps) {
		d.span.IsValidFunc = func() bool { return false }
		d.span.IsRecordingFunc = func() bool { return false }

		_, spanLog, span := d.lt.Start(t.Context(), "orders")
		defer span.End()
		spanLog.Info("invalid")

		got := logLine(t, d.buf.Bytes())
		assert.NotContains(t, got, "trace_id")
		assert.NotContains(t, got, "span_id")
		assert.Empty(t, d.span.SetAttributeCalls())
	})

	sweet.Run(t, "valid non-recording span still tags ids", recordingLT, func(t *testing.T, d recDeps) {
		d.span.IsRecordingFunc = func() bool { return false }

		_, spanLog, span := d.lt.Start(t.Context(), "orders")
		defer span.End()
		spanLog.Info("unsampled")

		got := logLine(t, d.buf.Bytes())
		assert.Equal(t, "trace-1", got["trace_id"])
		assert.Equal(t, "span-1", got["span_id"])
		assert.Empty(t, d.span.SetAttributeCalls())
	})

	sweet.Run(t, "nil span is safe", nilSpanLT, func(t *testing.T, d jsonDeps) {
		assert.NotPanics(t, func() {
			_, spanLog, span := d.lt.Start(t.Context(), "orders")
			defer span.End()
			require.NotNil(t, span)
			assert.False(t, span.IsValid())
			assert.Empty(t, span.TraceID())
			span.SetAttribute("k", "v")
			span.AddLink(observability.Link{})
			spanLog.Info("nil span")
		})

		got := logLine(t, d.buf.Bytes())
		assert.NotContains(t, got, "trace_id")
	})

	sweet.Run(t, "StartWith passes NewRoot", recordingLT, func(t *testing.T, d recDeps) {
		_, _, span := d.lt.StartWith(t.Context(), "op", observability.StartConfig{NewRoot: true})
		span.End()

		starts := d.tracer.StartCalls()
		require.Len(t, starts, 1)
		assert.True(t, starts[0].Cfg.NewRoot)
		assert.Empty(t, starts[0].Cfg.Links)
	})

	sweet.Run(t, "links do not imply new-root", recordingLT, func(t *testing.T, d recDeps) {
		cause := observability.Link{TraceID: "t", SpanID: "s"}
		_, _, span := d.lt.StartWith(t.Context(), "op", observability.StartConfig{Links: []observability.Link{cause}})
		span.End()

		starts := d.tracer.StartCalls()
		require.Len(t, starts, 1)
		assert.False(t, starts[0].Cfg.NewRoot)
		assert.Equal(t, []observability.Link{cause}, starts[0].Cfg.Links)
	})

	sweet.Run(t, "NewRoot and links compose", recordingLT, func(t *testing.T, d recDeps) {
		a := observability.Link{TraceID: "a", SpanID: "1"}
		b := observability.Link{TraceID: "b", SpanID: "2"}
		_, _, span := d.lt.StartWith(t.Context(), "op", observability.StartConfig{
			NewRoot: true,
			Links:   []observability.Link{a, b},
		})
		span.End()

		starts := d.tracer.StartCalls()
		require.Len(t, starts, 1)
		assert.Equal(t, observability.StartConfig{
			NewRoot: true,
			Links:   []observability.Link{a, b},
		}, starts[0].Cfg)
	})

	sweet.Run(t, "NewRoot logger uses returned span ids", recordingLT, func(t *testing.T, d recDeps) {
		d.span.TraceIDFunc = func() string { return "new-trace" }
		d.span.SpanIDFunc = func() string { return "new-span" }

		_, spanLog, span := d.lt.StartWith(t.Context(), "orders", observability.StartConfig{NewRoot: true})
		defer span.End()
		spanLog.Info("rooted")

		got := logLine(t, d.buf.Bytes())
		assert.Equal(t, "new-trace", got["trace_id"])
		assert.Equal(t, "new-span", got["span_id"])
	})

	sweet.Run(t, "Start returns the tracer span", recordingLT, func(t *testing.T, d recDeps) {
		_, _, span := d.lt.Start(t.Context(), "op")
		cause := observability.Link{TraceID: "t", SpanID: "s"}
		span.AddLink(cause)
		span.End()

		links := d.span.AddLinkCalls()
		require.Len(t, links, 1)
		assert.Equal(t, cause, links[0].Link)
	})
}

type jsonDeps struct {
	buf *bytes.Buffer
	lt  observability.LoggingTracer
}

type recDeps struct {
	buf    *bytes.Buffer
	lt     observability.LoggingTracer
	tracer *mocks.TracerMock
	span   *mocks.SpanMock
}

func zeroLT(*testing.T) observability.LoggingTracer {
	return observability.LoggingTracer{}
}

func jsonLT(t *testing.T) jsonDeps {
	t.Helper()

	buf := new(bytes.Buffer)
	log := slog.New(slog.NewJSONHandler(buf, nil))

	return jsonDeps{
		buf: buf,
		lt:  observability.LoggingTracer{Log: *log},
	}
}

func restoreDefault(t *testing.T) jsonDeps {
	t.Helper()

	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	return jsonDeps{buf: new(bytes.Buffer), lt: observability.LoggingTracer{}}
}

func recordingLT(t *testing.T) recDeps {
	t.Helper()

	span := &mocks.SpanMock{
		IsRecordingFunc: func() bool { return true },
		IsValidFunc:     func() bool { return true },
		TraceIDFunc:     func() string { return "trace-1" },
		SpanIDFunc:      func() string { return "span-1" },
	}
	tracer := &mocks.TracerMock{
		StartFunc: func(ctx context.Context, name string, _ observability.StartConfig) (context.Context, observability.Span) {
			return ctx, span
		},
	}

	d := jsonLT(t)
	d.lt.Tracer = tracer

	return recDeps{
		buf:    d.buf,
		lt:     d.lt,
		tracer: tracer,
		span:   span,
	}
}

func nilSpanLT(t *testing.T) jsonDeps {
	t.Helper()

	d := jsonLT(t)
	d.lt.Tracer = &mocks.TracerMock{
		StartFunc: func(ctx context.Context, name string, _ observability.StartConfig) (context.Context, observability.Span) {
			return ctx, nil
		},
	}

	return d
}

func logLine(t *testing.T, raw []byte) map[string]any {
	t.Helper()

	raw = bytes.TrimSpace(raw)
	require.NotEmpty(t, raw)

	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))

	return got
}
