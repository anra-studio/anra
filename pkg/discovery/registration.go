package discovery

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MaintainRegistration registers a card and renews its lease until cancellation.
// The returned context is canceled with a cause on a permanent registry rejection.
// The caller must call cleanup to stop renewal and unregister, even after the
// parent is canceled. Cleanup is safe to call more than once.
func (client *Client) MaintainRegistration(parent context.Context, record Record) (context.Context, func(), error) {
	lease := uuid.NewString()
	ttl, err := client.Register(parent, record, lease)
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithCancelCause(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(ttl / 3)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if nextTTL, err := client.Register(ctx, record, lease); err != nil {
					var rejection *HTTPError
					if errors.As(err, &rejection) && rejection.StatusCode >= 400 && rejection.StatusCode < 500 {
						cancel(err)
						return
					}
					// A temporary directory outage must not stop the agent's runtime.
				} else {
					ticker.Reset(nextTTL / 3)
				}
			}
		}
	}()
	cleanup := sync.OnceFunc(func() {
		cancel(context.Canceled)
		<-done
		ctx, stop := context.WithTimeout(context.Background(), 2*time.Second)
		defer stop()
		_ = client.Unregister(ctx, record.Name, lease)
	})
	return ctx, cleanup, nil
}
