// Package observability correlates slog with traces.
//
// Pass [LoggingTracer] into your own components. Give it a component at
// composition and call Start, naming the operation, inside each operation.
// Do not pass a slog.Logger down through application code — that drops
// Component and Start.
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
	"reflect"
)

const (
	attrComponent = "component"
	attrTraceID   = "trace_id"
	attrSpanID    = "span_id"
)

// LoggingTracer pairs a slog logger with an optional Tracer.
//
// Hold this type on services and handlers. Set the component once with
// Component; start spans with Start. Extract a slog.Logger only when
// calling an API that takes one.
//
// Component identifies the owner (a service, handler or job) and is stored
// as the slog field "component" and as a "component" span attribute when
// the span is recording. Start takes the operation's span name, which is
// required and independent of the component.
//
//	lt := observability.LoggingTracer{Log: log, Tracer: tr}.Component("orders")
//	svc := NewOrders(lt)
//	ctx, log, span := lt.Start(ctx, "Create")
//	defer span.End()
//	log.Info("creating") // component, trace_id, span_id
//
// Call Component at wiring, not per operation: it builds and caches the
// component logger, which is what keeps Start cheap.
type LoggingTracer struct {
	// Log is the base logger. A zero value (nil Handler) uses slog.Default.
	Log slog.Logger

	// Tracer starts spans from Start. Nil skips tracing and never panics.
	Tracer Tracer

	name string

	// named caches the logger for the component, built by Component. It is only used
	// while the effective base handler is the one it was built from, so
	// reassigning Log or calling slog.SetDefault after Component is still
	// honoured (the logger is rebuilt on demand).
	named     *slog.Logger
	namedBase slog.Handler
}

// Component sets the owner identity: the "component" field on logs and,
// for recording spans, the "component" span attribute. It does not affect
// span names; those are passed to Start.
//
// An empty component is ignored. Repeated calls overwrite the previous
// one.
//
// Component builds and caches the component logger, so a new component
// costs the allocations of slog.Logger.With once, and every later Logger
// or Start on the result is free of them. Setting the current component,
// or an empty one, allocates nothing. Call it once at composition.
func (lt LoggingTracer) Component(name string) LoggingTracer {
	if name == "" || name == lt.name {
		return lt
	}

	lt.name = name
	lt.buildNamed()

	return lt
}

// Logger returns a slog.Logger with the current Component applied as
// "component". It never panics: a zero Log uses slog.Default.
//
// Prefer passing LoggingTracer. Use Logger only when an API requires
// slog.Logger (for example rest.HTTP, which stores a value: Log:
// *lt.Logger()). Inside a span, use the logger Start returns — that
// logger is also tagged with trace_id and span_id.
func (lt LoggingTracer) Logger() *slog.Logger {
	if lt.name == "" {
		return lt.baseLog()
	}

	if lt.named != nil && sameHandler(lt.namedBase, lt.baseHandler()) {
		return lt.named
	}

	lt.buildNamed()

	return lt.named
}

// Start starts a span called name and returns a logger tagged with the
// component plus, when the span is valid, the span's trace_id and span_id.
// Invalid spans omit those fields rather than logging empty strings.
// Recording spans also get a "component" attribute when a component is
// set; non-recording spans do not.
//
// name is the operation, for example "Create" or "http.POST /orders". It
// is required: pass a constant, not a per-call formatted string.
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
// does not create a span and returns Logger with a no-op Span, without
// allocating. A nil ctx is treated as context.Background.
func (lt LoggingTracer) Start(ctx context.Context, name string, opts ...StartOption) (context.Context, *slog.Logger, Span) {
	if ctx == nil {
		ctx = context.Background()
	}

	if lt.Tracer == nil {
		return ctx, lt.Logger(), noopSpan{}
	}

	log := lt.Logger()
	span := Span(noopSpan{})

	var cfg StartConfig
	for _, opt := range opts {
		if opt == nil {
			continue
		}

		opt(&cfg)
	}

	ctx, s := lt.Tracer.Start(ctx, name, cfg)
	if s == nil {
		return ctx, log, span
	}

	span = s

	if span.IsRecording() && lt.name != "" {
		span.SetAttribute(attrComponent, lt.name)
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

// baseHandler is the handler baseLog would use, without allocating.
func (lt LoggingTracer) baseHandler() slog.Handler {
	if h := lt.Log.Handler(); h != nil {
		return h
	}

	return slog.Default().Handler()
}

// buildNamed derives the named logger from the current base logger and
// stores it on lt.
func (lt *LoggingTracer) buildNamed() {
	lt.namedBase = lt.baseHandler()
	lt.named = lt.baseLog().With(attrComponent, lt.name)
}

// sameHandler reports whether a and b are the same handler. Handlers whose
// dynamic type is not comparable are never treated as the same, so a
// stale cache is rebuilt rather than a comparison panicking.
func sameHandler(a, b slog.Handler) (same bool) {
	if a == nil || b == nil {
		return false
	}

	if !reflect.TypeOf(a).Comparable() || !reflect.TypeOf(b).Comparable() {
		return false
	}

	// A comparable struct type can still hold an interface field whose
	// dynamic value is not comparable; that panics at run time.
	defer func() {
		if recover() != nil {
			same = false
		}
	}()

	return a == b
}
