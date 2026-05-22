package bones

import (
	"context"

	"golang.org/x/sync/errgroup"
)

type Check interface {
	Healthy(context.Context) error
}

type Health struct {
	Checks []Check
}

func (h *Health) Healthy(ctx context.Context) error {
	g, ctx := errgroup.WithContext(ctx)
	for _, check := range h.Checks {
		g.Go(func() error {
			return check.Healthy(ctx)
		})
	}

	return g.Wait()
}
