package bones

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func RootCtx() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGINT)
}

var ErrUnknownContext = errors.New("unknown context type")

func CloseWithin(ctx context.Context, close func(ctx context.Context) error, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	return close(ctx)
}

// CloseWhenDone adapts common close functions to errgroup.Go's expected
// `func(context.Context) error` type
func CloseWhenDone(ctx context.Context, close any) func() error {
	switch c := close.(type) {
	case func():
	case func(context.Context) error:
	default:
		return func() error {
			return fmt.Errorf("%w %T", ErrUnknownContext, c)
		}
	}

	return func() error {
		<-ctx.Done()

		switch c := close.(type) {
		case func():
			c()
		case func(context.Context) error:
			return c(ctx)
		}

		return ErrUnknownContext
	}

}

// CloseServerWhenDone shuts the server down within the timeout when the context
// closes
//
//	gg, ctx := errgroup.WithContext(ctx)
//
//	gg.Go(serv.ListenAndServe)
//	gg.Go(CloseServerWhenDone(ctx, serv, time.Second))
func CloseServerWhenDone(ctx context.Context, server *http.Server, timeout time.Duration) func() error {
	return CloseWhenDone(ctx, func(ctx context.Context) error {
		return CloseWithin(ctx, server.Shutdown, timeout)
	})
}
