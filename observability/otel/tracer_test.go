package otel_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/barry-hennessy/bones/observability"
	otelobs "github.com/barry-hennessy/bones/observability/otel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

const (
	causeTraceID = "01000000000000000000000000000000"
	causeSpanID  = "0200000000000000"
)

func TestNewNilTracer(t *testing.T) {
	assert.Nil(t, otelobs.New(nil))
}

func TestNewStartsNamedRecordingSpan(t *testing.T) {
	tp, recorder := testProvider(t)

	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))

	tr := otelobs.New(tp.Tracer("test"))
	lt := observability.LoggingTracer{Log: *log, Tracer: tr}.Name("orders")

	ctx, spanLog, span := lt.Start(t.Context())
	spanLog.Info("creating")
	span.End()

	sc := trace.SpanFromContext(ctx).SpanContext()
	require.True(t, sc.IsValid())

	got := logLine(t, buf.Bytes())
	assert.Equal(t, "orders", got["component"])
	assert.Equal(t, sc.TraceID().String(), got["trace_id"])
	assert.Equal(t, sc.SpanID().String(), got["span_id"])

	spans := recorder.Ended()
	require.Len(t, spans, 1)
	assert.Equal(t, "orders", spans[0].Name())
	assert.Contains(t, spans[0].Attributes(), attribute.String("component", "orders"))
}

func TestNewAppliesStartOptions(t *testing.T) {
	tp, recorder := testProvider(t)

	tr := otelobs.New(tp.Tracer("test"), trace.WithSpanKind(trace.SpanKindServer))
	lt := observability.LoggingTracer{Tracer: tr}.Name("orders")

	_, _, span := lt.Start(t.Context())
	span.End()

	spans := recorder.Ended()
	require.Len(t, spans, 1)
	assert.Equal(t, trace.SpanKindServer, spans[0].SpanKind())
}

func TestStartParentsWithoutOptions(t *testing.T) {
	tp, recorder := testProvider(t)
	lt := observability.LoggingTracer{Tracer: otelobs.New(tp.Tracer("test"))}

	parentCtx, _, parent := lt.Name("parent").Start(t.Context())
	_, _, child := lt.Name("child").Start(parentCtx)
	child.End()
	parent.End()

	got := endedNamed(t, recorder, "child")
	assert.True(t, got.Parent().IsValid())
	assert.Equal(t, trace.SpanFromContext(parentCtx).SpanContext().TraceID(), got.SpanContext().TraceID())
	assert.Equal(t, trace.SpanFromContext(parentCtx).SpanContext().SpanID(), got.Parent().SpanID())
}

func TestWithNewRootIgnoresParent(t *testing.T) {
	tp, recorder := testProvider(t)

	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	lt := observability.LoggingTracer{Log: *log, Tracer: otelobs.New(tp.Tracer("test"))}

	parentCtx, _, parent := lt.Name("parent").Start(t.Context())
	parentTrace := parent.TraceID()
	require.NotEmpty(t, parentTrace)

	ctx, cancel := context.WithCancel(parentCtx)
	rootedCtx, spanLog, span := lt.Name("child").Start(ctx, observability.WithNewRoot())

	assert.Equal(t, parentTrace, trace.SpanFromContext(parentCtx).SpanContext().TraceID().String(),
		"Start must not mutate the input ctx")
	assert.NotEqual(t, parentTrace, span.TraceID())
	assert.NotEqual(t, parentTrace, trace.SpanFromContext(rootedCtx).SpanContext().TraceID().String())

	buf.Reset()
	spanLog.Info("rooted")
	gotLog := logLine(t, buf.Bytes())
	assert.Equal(t, span.TraceID(), gotLog["trace_id"])
	assert.Equal(t, span.SpanID(), gotLog["span_id"])
	assert.NotEqual(t, parentTrace, gotLog["trace_id"])

	cancel()
	select {
	case <-rootedCtx.Done():
	default:
		t.Fatal("new-root ctx should still cancel with the parent")
	}

	span.End()
	parent.End()

	child := endedNamed(t, recorder, "child")
	assert.False(t, child.Parent().IsValid())
	assert.NotEqual(t, parentTrace, child.SpanContext().TraceID().String())
}

func TestConstructorKindAppliesToNewRoot(t *testing.T) {
	tp, recorder := testProvider(t)
	tr := otelobs.New(tp.Tracer("test"), trace.WithSpanKind(trace.SpanKindServer))
	lt := observability.LoggingTracer{Tracer: tr}

	parentCtx, _, parent := lt.Name("parent").Start(t.Context())
	_, _, span := lt.Name("child").Start(parentCtx, observability.WithNewRoot())
	span.End()
	parent.End()

	child := endedNamed(t, recorder, "child")
	assert.False(t, child.Parent().IsValid())
	assert.Equal(t, trace.SpanKindServer, child.SpanKind())
}

func TestWithLinkRecordsRemoteCause(t *testing.T) {
	tp, recorder := testProvider(t)
	lt := observability.LoggingTracer{Tracer: otelobs.New(tp.Tracer("test"))}

	_, _, span := lt.Name("advance").Start(t.Context(),
		observability.WithNewRoot(),
		observability.WithLink(observability.Link{
			TraceID: causeTraceID,
			SpanID:  causeSpanID,
		}),
	)
	span.End()

	got := endedNamed(t, recorder, "advance")
	require.Len(t, got.Links(), 1)
	sc := got.Links()[0].SpanContext
	assert.Equal(t, causeTraceID, sc.TraceID().String())
	assert.Equal(t, causeSpanID, sc.SpanID().String())
	assert.True(t, sc.IsRemote())
}

func TestInvalidLinksAreSkipped(t *testing.T) {
	cases := []observability.Link{
		{},
		{TraceID: "nope", SpanID: "nope"},
		{TraceID: causeTraceID, SpanID: "short"},
		{TraceID: "short", SpanID: causeSpanID},
		{TraceID: causeTraceID, SpanID: ""},
		{TraceID: "", SpanID: causeSpanID},
	}

	for _, link := range cases {
		t.Run(link.TraceID+"/"+link.SpanID, func(t *testing.T) {
			tp, recorder := testProvider(t)
			lt := observability.LoggingTracer{Tracer: otelobs.New(tp.Tracer("test"))}

			assert.NotPanics(t, func() {
				_, _, span := lt.Name("advance").Start(t.Context(), observability.WithLink(link))
				span.End()
			})

			got := endedNamed(t, recorder, "advance")
			assert.Empty(t, got.Links())
		})
	}
}

func TestAddLinkAfterStart(t *testing.T) {
	tp, recorder := testProvider(t)
	lt := observability.LoggingTracer{Tracer: otelobs.New(tp.Tracer("test"))}

	_, _, span := lt.Name("advance").Start(t.Context())
	span.AddLink(observability.Link{TraceID: causeTraceID, SpanID: causeSpanID})
	span.End()

	got := endedNamed(t, recorder, "advance")
	require.Len(t, got.Links(), 1)
	assert.Equal(t, causeTraceID, got.Links()[0].SpanContext.TraceID().String())
	assert.True(t, got.Links()[0].SpanContext.IsRemote())
}

func TestAddLinkAfterEndIsNoop(t *testing.T) {
	tp, recorder := testProvider(t)
	lt := observability.LoggingTracer{Tracer: otelobs.New(tp.Tracer("test"))}

	_, _, span := lt.Name("advance").Start(t.Context())
	span.End()
	span.AddLink(observability.Link{TraceID: causeTraceID, SpanID: causeSpanID})

	got := endedNamed(t, recorder, "advance")
	assert.Empty(t, got.Links())
}

func TestAddLinkInvalidIsSkipped(t *testing.T) {
	tp, recorder := testProvider(t)
	lt := observability.LoggingTracer{Tracer: otelobs.New(tp.Tracer("test"))}

	_, _, span := lt.Name("advance").Start(t.Context())
	span.AddLink(observability.Link{TraceID: "nope", SpanID: "nope"})
	span.End()

	got := endedNamed(t, recorder, "advance")
	assert.Empty(t, got.Links())
}

func TestWithLinkWithoutNewRootIsChildAndLink(t *testing.T) {
	tp, recorder := testProvider(t)
	lt := observability.LoggingTracer{Tracer: otelobs.New(tp.Tracer("test"))}

	parentCtx, _, parent := lt.Name("parent").Start(t.Context())
	_, _, child := lt.Name("child").Start(parentCtx, observability.WithLink(observability.Link{
		TraceID: causeTraceID,
		SpanID:  causeSpanID,
	}))
	child.End()
	parent.End()

	got := endedNamed(t, recorder, "child")
	assert.True(t, got.Parent().IsValid())
	assert.Equal(t, trace.SpanFromContext(parentCtx).SpanContext().TraceID(), got.SpanContext().TraceID())
	assert.Equal(t, trace.SpanFromContext(parentCtx).SpanContext().SpanID(), got.Parent().SpanID())
	require.Len(t, got.Links(), 1)
	assert.Equal(t, causeTraceID, got.Links()[0].SpanContext.TraceID().String())
	assert.True(t, got.Links()[0].SpanContext.IsRemote())
}

func testProvider(t *testing.T) (*sdktrace.TracerProvider, *tracetest.SpanRecorder) {
	t.Helper()

	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() {
		require.NoError(t, tp.Shutdown(context.Background()))
	})

	return tp, recorder
}

func endedNamed(t *testing.T, recorder *tracetest.SpanRecorder, name string) sdktrace.ReadOnlySpan {
	t.Helper()

	for _, s := range recorder.Ended() {
		if s.Name() == name {
			return s
		}
	}

	t.Fatalf("no ended span named %q", name)

	return nil
}

func logLine(t *testing.T, raw []byte) map[string]any {
	t.Helper()

	raw = bytes.TrimSpace(raw)
	require.NotEmpty(t, raw)

	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))

	return got
}
