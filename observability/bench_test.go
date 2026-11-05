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

	b.Run("named no tracer", func(b *testing.B) {
		lt := observability.LoggingTracer{}.Component("orders")
		benchStart(b, ctx, lt)
	})

	b.Run("named custom log no tracer", func(b *testing.B) {
		log := slog.New(slog.NewJSONHandler(io.Discard, nil))
		lt := observability.LoggingTracer{Log: *log}.Component("orders")
		benchStart(b, ctx, lt)
	})

	b.Run("otel noop provider", func(b *testing.B) {
		tr := otelobs.New(noop.NewTracerProvider().Tracer("bench"))
		lt := observability.LoggingTracer{Tracer: tr}.Component("orders")
		benchStart(b, ctx, lt)
	})

	b.Run("otel sdk in-memory exporter", func(b *testing.B) {
		exp := tracetest.NewInMemoryExporter()
		tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
		b.Cleanup(func() { _ = tp.Shutdown(ctx) })

		lt := observability.LoggingTracer{Tracer: otelobs.New(tp.Tracer("bench"))}.Component("orders")

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

		lt := observability.LoggingTracer{Tracer: otelobs.New(tp.Tracer("bench"))}.Component("jobs")
		link := observability.Link{TraceID: "01000000000000000000000000000000", SpanID: "0200000000000000"}

		b.ReportAllocs()
		b.ResetTimer()

		for b.Loop() {
			_, _, span := lt.Start(ctx, "op", observability.WithNewRoot(), observability.WithLink(link))
			span.End()
		}
	})
}

func BenchmarkNameStart(b *testing.B) {
	ctx := context.Background()

	b.Run("pre-named reused", func(b *testing.B) {
		lt := observability.LoggingTracer{}.Component("orders")
		benchStart(b, ctx, lt)
	})

	// The praytel hot-path shape: lt.Component(x).Start(ctx, name) on every call.
	b.Run("same name each call", func(b *testing.B) {
		lt := observability.LoggingTracer{}.Component("orders")

		b.ReportAllocs()
		b.ResetTimer()

		for b.Loop() {
			_, _, span := lt.Component("orders").Start(ctx, "orders")
			span.End()
		}
	})

	b.Run("new name each call", func(b *testing.B) {
		lt := observability.LoggingTracer{}.Component("orders")

		b.ReportAllocs()
		b.ResetTimer()

		for b.Loop() {
			_, _, span := lt.Component("orders.get").Start(ctx, "orders.get")
			span.End()
		}
	})
}

func BenchmarkName(b *testing.B) {
	lt := observability.LoggingTracer{}.Component("orders")

	b.Run("same name", func(b *testing.B) {
		b.ReportAllocs()

		for b.Loop() {
			lt = lt.Component("orders")
		}
	})

	b.Run("new name", func(b *testing.B) {
		names := [2]string{"orders.get", "orders.list"}
		i := 0

		b.ReportAllocs()

		for b.Loop() {
			lt = lt.Component(names[i&1])
			i++
		}
	})

	_ = lt
}

func BenchmarkLogger(b *testing.B) {
	b.Run("unnamed", func(b *testing.B) {
		lt := observability.LoggingTracer{}
		b.ReportAllocs()

		for b.Loop() {
			_ = lt.Logger()
		}
	})

	b.Run("named", func(b *testing.B) {
		lt := observability.LoggingTracer{}.Component("orders")
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
