// Package observability correlates slog with traces.
//
// Pass [LoggingTracer] into your own components. Name it at composition
// (or on the child) and call Start inside each operation. Do not pass a
// slog.Logger down through application code — that drops Name and Start.
// Call [LoggingTracer.Logger] only at a boundary that requires
// slog.Logger, such as rest.HTTP.
//
// LoggingTracer is safe to use zero-valued: Logger falls back to
// [log/slog.Default], and Start skips tracing when Tracer is nil.
//
// Tracer is a Bones interface. Do not import a tracing SDK from this
// package; wrap OpenTelemetry via
// [github.com/barry-hennessy/bones/observability/otel] or supply your
// own adapter (including OpenTracing).
package observability

import (
	"context"
	"log/slog"
)

const (
	attrComponent = "component"
	attrTraceID   = "trace_id"
	attrSpanID    = "span_id"
)

// LoggingTracer pairs a slog logger with an optional Tracer.
//
// Hold this type on services and handlers. Derive children with Name;
// start spans with Start. Extract a slog.Logger only when calling an API
// that takes one.
//
// Name sets the identity used for both the logger and spans started by
// Start. The name is stored as the slog field "component", used as the
// span name, and set as a "component" span attribute when the span is
// recording.
//
//	lt := observability.LoggingTracer{Log: log, Tracer: tr}.Name("orders")
//	svc := NewOrders(lt)
//	ctx, log, span := lt.Start(ctx)
//	defer span.End()
//	log.Info("creating") // component, trace_id, span_id
//
// Name overwrites; it does not join. Call Name at wiring, or immediately
// before Start when the span should use a more specific operation name:
//
//	ctx, log, span := lt.Name("http.POST /orders").Start(ctx, WithNewRoot())
//	defer span.End()
type LoggingTracer struct {
	// Log is the base logger. A zero value (nil Handler) uses slog.Default.
	Log slog.Logger

	// Tracer starts spans from Start. Nil skips tracing and never panics.
	Tracer Tracer

	name string
}

// Name sets the identity for both the logger (component field) and spans
// started by Start (span name and component attribute).
//
// An empty name is ignored. Repeated calls overwrite the previous name.
func (lt LoggingTracer) Name(name string) LoggingTracer {
	if name == "" {
		return lt
	}

	lt.name = name

	return lt
}

// Logger returns a slog.Logger with the current Name applied as
// "component". It never panics: a zero Log uses slog.Default.
//
// Prefer passing LoggingTracer. Use Logger only when an API requires
// slog.Logger (for example rest.HTTP, which stores a value: Log:
// *lt.Logger()). Inside a span, use the logger Start returns — that
// logger is also tagged with trace_id and span_id.
func (lt LoggingTracer) Logger() *slog.Logger {
	log := lt.baseLog()
	if lt.name == "" {
		return log
	}

	return log.With(attrComponent, lt.name)
}

// Start starts a span named from Name and returns a logger tagged with
// that name plus, when the span is valid, the span's trace_id and span_id.
// Invalid spans omit those fields rather than logging empty strings.
// Recording spans also get a "component" attribute; non-recording spans
// do not.
//
// With no options, Start parents via the backing library on ctx. Pass
// [WithNewRoot] for a new trace (cancellation is kept). Pass [WithLink]
// for causality to a stored span; it may be repeated and does not imply
// new-root.
//
// The returned logger can be used with Info/Error (no Context required)
// for this span. Call span.End when the span finishes, typically via
// defer. The Span is never a nil interface.
//
// Start does not mutate ctx. Use the returned context so later Starts
// parent correctly. The returned ctx is whatever [Tracer.Start] returned;
// LoggingTracer does not copy span ids onto it. If Tracer is nil, Start
// does not create a span and returns Logger with a no-op Span. A nil ctx
// is treated as context.Background.
func (lt LoggingTracer) Start(ctx context.Context, opts ...StartOption) (context.Context, *slog.Logger, Span) {
	if ctx == nil {
		ctx = context.Background()
	}

	log := lt.Logger()
	span := Span(noopSpan{})

	if lt.Tracer == nil {
		return ctx, log, span
	}

	var cfg StartConfig
	for _, opt := range opts {
		if opt == nil {
			continue
		}

		opt(&cfg)
	}

	ctx, s := lt.Tracer.Start(ctx, lt.name, cfg)
	if s == nil {
		return ctx, log, span
	}

	span = s

	if span.IsRecording() && lt.name != "" {
		span.SetAttribute(attrComponent, lt.name)
	}

	if span.IsValid() {
		log = log.With(
			slog.String(attrTraceID, span.TraceID()),
			slog.String(attrSpanID, span.SpanID()),
		)
	}

	return ctx, log, span
}

func (lt LoggingTracer) baseLog() *slog.Logger {
	if lt.Log.Handler() == nil {
		return slog.Default()
	}

	log := lt.Log

	return &log
}
