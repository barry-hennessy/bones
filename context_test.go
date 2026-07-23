package bones_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/barry-hennessy/bones"
	"github.com/stretchr/testify/assert"
	"golang.org/x/sync/errgroup"
)

func TestCloseWhenDone(t *testing.T) {
	t.Run("regular close fn", func(t *testing.T) {
		closed := false
		// A close function closing over a closed variable that initially is...
		// not closed
		close := func() {
			closed = true
		}

		ctx, cancelCtx := context.WithCancel(context.Background())

		gg, ctx := errgroup.WithContext(ctx)
		gg.Go(bones.CloseWhenDone(ctx, close))

		cancelCtx()
		gg.Wait()

		assert.True(t, closed)
	})

	t.Run("close fn that takes a context", func(t *testing.T) {
		closed := false
		// A close function closing over a closed variable that initially is...
		// not closed
		close := func(ctx context.Context) error {
			closed = true
			return nil
		}

		ctx, cancelCtx := context.WithCancel(context.Background())

		gg, ctx := errgroup.WithContext(ctx)
		gg.Go(bones.CloseWhenDone(ctx, close))

		cancelCtx()
		gg.Wait()

		assert.True(t, closed)
	})

	t.Run("fails fast on a non close fn", func(t *testing.T) {
		closed := false
		// A close function closing over a closed variable that initially is...
		// not closed
		close := func(t *testing.T) {
			closed = true
		}

		ctx, cancelCtx := context.WithCancel(context.Background())

		gg, ctx := errgroup.WithContext(ctx)
		gg.Go(bones.CloseWhenDone(ctx, close))

		cancelCtx()
		err := gg.Wait()

		// Nothing would've been closed because the whole thing wouldn't
		// have started up
		assert.False(t, closed)
		assert.ErrorIs(t, err, bones.ErrUnknownContext)
	})
}

func TestCloseServerWhenDone(t *testing.T) {
	t.Run("closes within timeframe", func(t *testing.T) {
		ctx, cancel := bones.RootCtx()

		s := &http.Server{}

		gg, ctx := errgroup.WithContext(ctx)
		gg.Go(bones.CloseServerWhenDone(ctx, s, 5*time.Millisecond))

		cancel()
		err := gg.Wait()

		assert.NoError(t, err)
		assert.Error(t, s.ListenAndServeTLS("foo", "bar"), http.ErrServerClosed)
	})
}
