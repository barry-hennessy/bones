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

		ctx, log, span := lt.Start(t.Context())
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
			ctx, log, span := lt.Start(nil)
			defer span.End()
			assert.Equal(t, context.Background(), ctx)
			log.Info("nil ctx")
		})
	})

	sweet.Run(t, "nil ctx passed to tracer", recordingLT, func(t *testing.T, d recDeps) {
		_, _, span := d.lt.Start(nil)
		span.End()

		starts := d.tracer.StartCalls()
		require.Len(t, starts, 1)
		assert.Equal(t, context.Background(), starts[0].Ctx)
	})

	sweet.Run(t, "zero Log uses slog.Default", restoreDefault, func(t *testing.T, d jsonDeps) {
		slog.SetDefault(slog.New(slog.NewJSONHandler(d.buf, nil)))

		d.lt.Name("orders").Logger().Info("from default")

		got := logLine(t, d.buf.Bytes())
		assert.Equal(t, "from default", got["msg"])
		assert.Equal(t, "orders", got["component"])
	})

	sweet.Run(t, "reads slog.Default on each call", restoreDefault, func(t *testing.T, d jsonDeps) {
		lt := d.lt.Name("orders")

		slog.SetDefault(slog.New(slog.NewJSONHandler(d.buf, nil)))

		lt.Logger().Info("after setdefault")

		got := logLine(t, d.buf.Bytes())
		assert.Equal(t, "after setdefault", got["msg"])
		assert.Equal(t, "orders", got["component"])
	})

	sweet.Run(t, "Name overwrites", jsonLT, func(t *testing.T, d jsonDeps) {
		d.lt.Name("orders").Name("orders.Create").Logger().Info("named")

		got := logLine(t, d.buf.Bytes())
		assert.Equal(t, "orders.Create", got["component"])

		// @TODO: Why do we test this here?
		assert.NotContains(t, got, "trace_id")
	})

	sweet.Run(t, "empty Name ignored", jsonLT, func(t *testing.T, d jsonDeps) {
		d.lt.Name("orders").Name("").Logger().Info("kept")

		got := logLine(t, d.buf.Bytes())
		assert.Equal(t, "orders", got["component"])
	})

	sweet.Run(t, "nil tracer skips tracing", jsonLT, func(t *testing.T, d jsonDeps) {
		lt := d.lt.Name("orders")
		ctx, spanLog, span := lt.Start(t.Context())
		defer span.End()

		spanLog.Info("no tracer")

		got := logLine(t, d.buf.Bytes())
		assert.Equal(t, "orders", got["component"])
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

	sweet.Run(t, "nil tracer WithNewRoot does not panic", jsonLT, func(t *testing.T, d jsonDeps) {
		ctx, _, span := d.lt.Start(t.Context(), observability.WithNewRoot())
		defer span.End()

		assert.Equal(t, t.Context(), ctx)
		require.NotNil(t, span)
		assert.False(t, span.IsValid())
	})

	sweet.Run(t, "Start tags logger and span", recordingLT, func(t *testing.T, d recDeps) {
		_, spanLog, span := d.lt.Name("orders").Start(t.Context())
		spanLog.Info("creating")
		span.End()

		got := logLine(t, d.buf.Bytes())
		assert.Equal(t, "creating", got["msg"])
		assert.Equal(t, "orders", got["component"])
		assert.Equal(t, "trace-1", got["trace_id"])
		assert.Equal(t, "span-1", got["span_id"])

		starts := d.tracer.StartCalls()
		require.Len(t, starts, 1)
		assert.Equal(t, "orders", starts[0].Name)
		assert.Equal(t, observability.StartConfig{}, starts[0].Cfg)

		attrs := d.span.SetAttributeCalls()
		require.Len(t, attrs, 1)
		assert.Equal(t, "component", attrs[0].Key)
		assert.Equal(t, "orders", attrs[0].Value)

		require.Len(t, d.span.EndCalls(), 1)
	})

	sweet.Run(t, "Start returns the tracer ctx", recordingLT, func(t *testing.T, d recDeps) {
		ctx, _, span := d.lt.Name("orders").Start(t.Context())
		span.End()

		assert.Equal(t, t.Context(), ctx)
	})

	sweet.Run(t, "Name is the span name", recordingLT, func(t *testing.T, d recDeps) {
		_, _, span := d.lt.Name("http.POST /orders").Start(t.Context())
		span.End()

		starts := d.tracer.StartCalls()
		require.Len(t, starts, 1)
		assert.Equal(t, "http.POST /orders", starts[0].Name)

		attrs := d.span.SetAttributeCalls()
		require.Len(t, attrs, 1)
		assert.Equal(t, "http.POST /orders", attrs[0].Value)
	})

	sweet.Run(t, "invalid span omits ids", recordingLT, func(t *testing.T, d recDeps) {
		d.span.IsValidFunc = func() bool { return false }
		d.span.IsRecordingFunc = func() bool { return false }

		_, spanLog, span := d.lt.Name("orders").Start(t.Context())
		defer span.End()
		spanLog.Info("invalid")

		got := logLine(t, d.buf.Bytes())
		assert.Equal(t, "orders", got["component"])
		assert.NotContains(t, got, "trace_id")
		assert.NotContains(t, got, "span_id")
		assert.Empty(t, d.span.SetAttributeCalls())
	})

	sweet.Run(t, "valid non-recording span still tags ids", recordingLT, func(t *testing.T, d recDeps) {
		d.span.IsRecordingFunc = func() bool { return false }

		_, spanLog, span := d.lt.Name("orders").Start(t.Context())
		defer span.End()
		spanLog.Info("unsampled")

		got := logLine(t, d.buf.Bytes())
		assert.Equal(t, "orders", got["component"])
		assert.Equal(t, "trace-1", got["trace_id"])
		assert.Equal(t, "span-1", got["span_id"])
		assert.Empty(t, d.span.SetAttributeCalls())
	})

	sweet.Run(t, "unnamed Start skips component attribute", recordingLT, func(t *testing.T, d recDeps) {
		_, _, span := d.lt.Start(t.Context())
		span.End()

		starts := d.tracer.StartCalls()
		require.Len(t, starts, 1)
		assert.Equal(t, "", starts[0].Name)
		assert.Empty(t, d.span.SetAttributeCalls())
	})

	sweet.Run(t, "nil span is safe", nilSpanLT, func(t *testing.T, d jsonDeps) {
		assert.NotPanics(t, func() {
			_, spanLog, span := d.lt.Name("orders").Start(t.Context())
			defer span.End()
			require.NotNil(t, span)
			assert.False(t, span.IsValid())
			assert.Empty(t, span.TraceID())
			span.SetAttribute("k", "v")
			span.AddLink(observability.Link{})
			spanLog.Info("nil span")
		})

		got := logLine(t, d.buf.Bytes())
		assert.Equal(t, "orders", got["component"])
		assert.NotContains(t, got, "trace_id")
	})

	sweet.Run(t, "WithNewRoot folds into StartConfig", recordingLT, func(t *testing.T, d recDeps) {
		_, _, span := d.lt.Start(t.Context(), observability.WithNewRoot())
		span.End()

		starts := d.tracer.StartCalls()
		require.Len(t, starts, 1)
		assert.True(t, starts[0].Cfg.NewRoot)
		assert.Empty(t, starts[0].Cfg.Links)
	})

	sweet.Run(t, "WithLink does not imply new-root", recordingLT, func(t *testing.T, d recDeps) {
		cause := observability.Link{TraceID: "t", SpanID: "s"}
		_, _, span := d.lt.Start(t.Context(), observability.WithLink(cause))
		span.End()

		starts := d.tracer.StartCalls()
		require.Len(t, starts, 1)
		assert.False(t, starts[0].Cfg.NewRoot)
		assert.Equal(t, []observability.Link{cause}, starts[0].Cfg.Links)
	})

	sweet.Run(t, "WithNewRoot and WithLink compose", recordingLT, func(t *testing.T, d recDeps) {
		a := observability.Link{TraceID: "a", SpanID: "1"}
		b := observability.Link{TraceID: "b", SpanID: "2"}
		_, _, span := d.lt.Start(t.Context(),
			observability.WithNewRoot(),
			observability.WithLink(a),
			observability.WithLink(b),
		)
		span.End()

		starts := d.tracer.StartCalls()
		require.Len(t, starts, 1)
		assert.Equal(t, observability.StartConfig{
			NewRoot: true,
			Links:   []observability.Link{a, b},
		}, starts[0].Cfg)
	})

	sweet.Run(t, "WithNewRoot logger uses returned span ids", recordingLT, func(t *testing.T, d recDeps) {
		d.span.TraceIDFunc = func() string { return "new-trace" }
		d.span.SpanIDFunc = func() string { return "new-span" }

		_, spanLog, span := d.lt.Name("orders").Start(t.Context(), observability.WithNewRoot())
		defer span.End()
		spanLog.Info("rooted")

		got := logLine(t, d.buf.Bytes())
		assert.Equal(t, "new-trace", got["trace_id"])
		assert.Equal(t, "new-span", got["span_id"])
	})

	sweet.Run(t, "Start returns the tracer span", recordingLT, func(t *testing.T, d recDeps) {
		_, _, span := d.lt.Start(t.Context())
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
