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

// StartConfig is the implementer view of a Start call. LoggingTracer
// folds Start options into this struct. Domain and workflow code should
// not construct it; they call WithNewRoot and WithLink.
type StartConfig struct {
	NewRoot bool
	Links   []Link
}

// StartOption configures a single LoggingTracer.Start call.
type StartOption func(*StartConfig)

// WithNewRoot starts a new trace, ignoring any parent span identity on
// ctx while keeping the rest of ctx (including cancellation).
func WithNewRoot() StartOption {
	return func(cfg *StartConfig) {
		cfg.NewRoot = true
	}
}

// WithLink attaches a link to a stored span. It may be repeated. It does
// not imply new-root; pass WithNewRoot as well when the span should be
// a new trace that still cites a cause.
func WithLink(l Link) StartOption {
	return func(cfg *StartConfig) {
		cfg.Links = append(cfg.Links, l)
	}
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
