package observability

import "context"

//go:generate go run github.com/matryer/moq@latest -stub -pkg mocks -out mocks/tracer_moq.go . Tracer Span

// Tracer starts named spans.
//
// Bones does not import a tracing SDK. Wrap OpenTelemetry (see the otel
// subpackage) or OpenTracing in an implementation of this interface.
// Start puts span identity on the returned ctx via the backing library.
// LoggingTracer returns that ctx unchanged.
type Tracer interface {
	Start(ctx context.Context, name string, cfg StartConfig) (context.Context, Span)
}

// Span is one operation in a trace. All methods must be safe to call when
// the span is not recording, has an invalid context, or has already ended.
type Span interface {
	End()
	IsRecording() bool
	SetAttribute(key, value string)
	AddLink(link Link)
	TraceID() string
	SpanID() string
	IsValid() bool
}

// Link is causality to a stored span (for example a job row), identified
// by portable hex ids. It is not a remote parent or a traceparent header.
// Empty or invalid ids are skipped by the adapter; they do not fail Start.
type Link struct {
	TraceID string
	SpanID  string
}

// StartConfig configures a single span start. Pass it to
// [LoggingTracer.StartWith]; [LoggingTracer.Start] uses the zero value.
// It is also what a Tracer receives.
type StartConfig struct {
	// NewRoot starts a new trace, ignoring any parent span identity on ctx
	// while keeping the rest of ctx (including cancellation).
	NewRoot bool

	// Links cite stored spans as causes. Links do not imply NewRoot; set
	// both when the span should be a new trace that still cites a cause.
	Links []Link
}

// noopSpan is returned when there is no tracer or the tracer returned nil.
// It is a concrete type so Start never yields a nil Span interface.
type noopSpan struct{}

func (noopSpan) End()                        {}
func (noopSpan) IsRecording() bool           { return false }
func (noopSpan) SetAttribute(string, string) {}
func (noopSpan) AddLink(Link)                {}
func (noopSpan) TraceID() string             { return "" }
func (noopSpan) SpanID() string              { return "" }
func (noopSpan) IsValid() bool               { return false }

var _ Span = noopSpan{}
