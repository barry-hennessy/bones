package observability_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/barry-hennessy/bones/observability"
	otelobs "github.com/barry-hennessy/bones/observability/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace/noop"
)

// Run with: go test ./observability -run '^$' -bench . -benchmem

func BenchmarkStartEnd(b *testing.B) {
	ctx := context.Background()

	b.Run("zero value", func(b *testing.B) {
		lt := observability.LoggingTracer{}
		benchStart(b, ctx, lt)
	})

	b.Run("custom log no tracer", func(b *testing.B) {
		log := slog.New(slog.NewJSONHandler(io.Discard, nil))
		lt := observability.LoggingTracer{Log: *log}
		benchStart(b, ctx, lt)
	})

	b.Run("otel noop provider", func(b *testing.B) {
		tr := otelobs.New(noop.NewTracerProvider().Tracer("bench"))
		lt := observability.LoggingTracer{Tracer: tr}
		benchStart(b, ctx, lt)
	})

	b.Run("otel sdk in-memory exporter", func(b *testing.B) {
		exp := tracetest.NewInMemoryExporter()
		tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
		b.Cleanup(func() { _ = tp.Shutdown(ctx) })

		lt := observability.LoggingTracer{Tracer: otelobs.New(tp.Tracer("bench"))}

		b.ReportAllocs()
		b.ResetTimer()

		n := 0

		for b.Loop() {
			_, _, span := lt.Start(ctx, "op")
			span.End()

			// Bound memory held by the exporter without cloning spans per iteration.
			if n++; n%1024 == 0 {
				exp.Reset()
			}
		}
	})

	b.Run("otel sdk with new root and link", func(b *testing.B) {
		tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(tracetest.NewInMemoryExporter()))
		b.Cleanup(func() { _ = tp.Shutdown(ctx) })

		lt := observability.LoggingTracer{Tracer: otelobs.New(tp.Tracer("bench"))}
		link := observability.Link{TraceID: "01000000000000000000000000000000", SpanID: "0200000000000000"}

		b.ReportAllocs()
		b.ResetTimer()

		for b.Loop() {
			_, _, span := lt.StartWith(ctx, "op", observability.StartConfig{NewRoot: true, Links: []observability.Link{link}})
			span.End()
		}
	})
}

func BenchmarkLogger(b *testing.B) {
	b.Run("unnamed", func(b *testing.B) {
		lt := observability.LoggingTracer{}
		b.ReportAllocs()

		for b.Loop() {
			_ = lt.Logger()
		}
	})
}

func benchStart(b *testing.B, ctx context.Context, lt observability.LoggingTracer) {
	b.Helper()
	b.ReportAllocs()

	for b.Loop() {
		_, _, span := lt.Start(ctx, "op")
		span.End()
	}
}

// BenchmarkStartConfig isolates the cost of StartWith's config. The stub
// tracer does no work, so the numbers are LoggingTracer's own.
func BenchmarkStartConfig(b *testing.B) {
	ctx := context.Background()
	link := observability.Link{TraceID: "01000000000000000000000000000000", SpanID: "0200000000000000"}

	for _, tracer := range []struct {
		name string
		lt   observability.LoggingTracer
	}{
		{"no tracer", observability.LoggingTracer{}},
		{"stub tracer", observability.LoggingTracer{Tracer: stubTracer{}}},
	} {
		b.Run(tracer.name+"/none", func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				_, _, span := tracer.lt.Start(ctx, "op")
				span.End()
			}
		})

		b.Run(tracer.name+"/new root", func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				_, _, span := tracer.lt.StartWith(ctx, "op", observability.StartConfig{NewRoot: true})
				span.End()
			}
		})

		b.Run(tracer.name+"/one link", func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				_, _, span := tracer.lt.StartWith(ctx, "op", observability.StartConfig{Links: []observability.Link{link}})
				span.End()
			}
		})

		b.Run(tracer.name+"/new root and link", func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				_, _, span := tracer.lt.StartWith(ctx, "op", observability.StartConfig{NewRoot: true, Links: []observability.Link{link}})
				span.End()
			}
		})
	}
}

type stubTracer struct{}

func (stubTracer) Start(ctx context.Context, _ string, _ observability.StartConfig) (context.Context, observability.Span) {
	return ctx, stubSpan{}
}

type stubSpan struct{}

func (stubSpan) End()                        {}
func (stubSpan) IsRecording() bool           { return false }
func (stubSpan) SetAttribute(string, string) {}
func (stubSpan) AddLink(observability.Link)  {}
func (stubSpan) TraceID() string             { return "" }
func (stubSpan) SpanID() string              { return "" }
func (stubSpan) IsValid() bool               { return false }
