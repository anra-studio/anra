package agent

import (
	"context"
	"errors"
)

var ErrClosing = errors.New("agent: shutting down; request was not accepted")

// Shutdown stops admission, cancels active reasoning and waits for its terminal
// cleanup. It never holds the admission lock while waiting. Owners must drain
// before closing the tape: A2A execution can outlive its HTTP request handler.
func (a *Anra) Shutdown(ctx context.Context) error {
	a.mu.Lock()
	a.execution.closing = true
	cancel, done := a.execution.cancel, a.execution.done
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
