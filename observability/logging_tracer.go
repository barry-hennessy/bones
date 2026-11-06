// Package observability correlates slog with traces.
//
// Pass [LoggingTracer] into your own components and call Start, naming the
// operation, inside each operation. Do not pass a slog.Logger down through
// application code — that drops Start. Call [LoggingTracer.Logger] only at
// a boundary that requires slog.Logger, such as rest.HTTP.
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
	attrTraceID = "trace_id"
	attrSpanID  = "span_id"
)

// LoggingTracer pairs a slog logger with an optional Tracer.
//
// Hold this type on services and handlers and start spans with Start.
// Extract a slog.Logger only when calling an API that takes one.
//
//	lt := observability.LoggingTracer{Log: log, Tracer: tr}
//	svc := NewOrders(lt)
//	ctx, log, span := lt.Start(ctx, "Create")
//	defer span.End()
//	log.Info("creating") // trace_id, span_id
type LoggingTracer struct {
	// Log is the base logger. A zero value (nil Handler) uses slog.Default.
	Log slog.Logger

	// Tracer starts spans from Start. Nil skips tracing and never panics.
	Tracer Tracer
}

// Logger returns the base logger. It never panics: a zero Log uses
// slog.Default.
//
// Prefer passing LoggingTracer. Use Logger only when an API requires
// slog.Logger (for example rest.HTTP, which stores a value: Log:
// *lt.Logger()). Inside a span, use the logger Start returns — that
// logger is also tagged with trace_id and span_id.
func (lt LoggingTracer) Logger() *slog.Logger {
	return lt.baseLog()
}

// Start starts a span called name and returns a logger that, when the span
// is valid, is tagged with the span's trace_id and span_id. Invalid spans
// omit those fields rather than logging empty strings.
//
// name is the operation, for example "Create" or "http.POST /orders". It
// is required: pass a constant, not a per-call formatted string.
//
// Start parents via the backing library on ctx. Use [LoggingTracer.StartWith]
// for a new trace or for links.
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
func (lt LoggingTracer) Start(ctx context.Context, name string) (context.Context, *slog.Logger, Span) {
	return lt.StartWith(ctx, name, StartConfig{})
}

// StartWith is Start with a [StartConfig]. Set NewRoot for a new trace
// (cancellation is kept). Set Links for causality to stored spans; links
// do not imply NewRoot.
func (lt LoggingTracer) StartWith(ctx context.Context, name string, cfg StartConfig) (context.Context, *slog.Logger, Span) {
	if ctx == nil {
		ctx = context.Background()
	}

	log := lt.baseLog()

	if lt.Tracer == nil {
		return ctx, log, noopSpan{}
	}

	ctx, span := lt.Tracer.Start(ctx, name, cfg)
	if span == nil {
		return ctx, log, noopSpan{}
	}

	if span.IsValid() {
		// Equivalent to log.With(...) but skips boxing each Attr into an any.
		log = slog.New(log.Handler().WithAttrs([]slog.Attr{
			slog.String(attrTraceID, span.TraceID()),
			slog.String(attrSpanID, span.SpanID()),
		}))
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
