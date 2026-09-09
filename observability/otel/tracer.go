// Package otel adapts OpenTelemetry tracers to [observability.Tracer].
package otel

import (
	"context"

	"github.com/barry-hennessy/bones/observability"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// New wraps an OpenTelemetry tracer as an [observability.Tracer]. Extra Start
// options (for example trace.WithSpanKind) are applied to every span.
// A nil tracer yields a nil [observability.Tracer].
func New(t trace.Tracer, opts ...trace.SpanStartOption) observability.Tracer {
	if t == nil {
		return nil
	}

	return tracer{inner: t, opts: opts}
}

type tracer struct {
	inner trace.Tracer
	opts  []trace.SpanStartOption
}

func (t tracer) Start(ctx context.Context, name string, cfg observability.StartConfig) (context.Context, observability.Span) {
	opts := make([]trace.SpanStartOption, 0, len(t.opts)+2)
	opts = append(opts, t.opts...)

	if cfg.NewRoot {
		opts = append(opts, trace.WithNewRoot())
	}

	if links := otelLinks(cfg.Links); len(links) > 0 {
		opts = append(opts, trace.WithLinks(links...))
	}

	ctx, s := t.inner.Start(ctx, name, opts...)

	return ctx, span{inner: s}
}

type span struct {
	inner trace.Span
}

func (s span) End() {
	s.inner.End()
}

func (s span) IsRecording() bool {
	return s.inner.IsRecording()
}

func (s span) SetAttribute(key, value string) {
	s.inner.SetAttributes(attribute.String(key, value))
}

func (s span) AddLink(l observability.Link) {
	sc, ok := remoteSpanContext(l)
	if !ok {
		return
	}

	s.inner.AddLink(trace.Link{SpanContext: sc})
}

func (s span) TraceID() string {
	return s.inner.SpanContext().TraceID().String()
}

func (s span) SpanID() string {
	return s.inner.SpanContext().SpanID().String()
}

func (s span) IsValid() bool {
	return s.inner.SpanContext().IsValid()
}

func otelLinks(in []observability.Link) []trace.Link {
	var out []trace.Link
	for _, l := range in {
		sc, ok := remoteSpanContext(l)
		if !ok {
			continue
		}

		out = append(out, trace.Link{SpanContext: sc})
	}

	return out
}

func remoteSpanContext(l observability.Link) (trace.SpanContext, bool) {
	tid, err := trace.TraceIDFromHex(l.TraceID)
	if err != nil {
		return trace.SpanContext{}, false
	}

	sid, err := trace.SpanIDFromHex(l.SpanID)
	if err != nil {
		return trace.SpanContext{}, false
	}

	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: tid,
		SpanID:  sid,
		Remote:  true,
	}), true
}

var (
	_ observability.Tracer = tracer{}
	_ observability.Span   = span{}
)
