package discovery

import (
	"context"
	"errors"
	"sync"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	pv "github.com/scbizu/anra/gen/anra/peer/v1"
	rv "github.com/scbizu/anra/gen/anra/registry/v1"
	peermodel "github.com/scbizu/anra/pkg/model/peer"
	"google.golang.org/protobuf/proto"
)

// Registration owns one runtime's presence. Close disconnects without removing
// the Peer. Update serializes metadata changes with recovery and heartbeat calls.
type Registration struct {
	client                     *Client
	mu                         sync.Mutex
	value                      *pv.Peer
	id                         string
	runtimeID, token, revision string
	ctx                        context.Context
	cancel                     context.CancelCauseFunc
	done                       chan struct{}
	close                      func()
	onChange                   func(*pv.Peer)
}

func (s *Registration) Context() context.Context { return s.ctx }
func (s *Registration) Close()                   { s.close() }

// Start creates one runtime ownership session and begins heartbeat maintenance.
// The optional callback receives reconciled metadata, including a commit whose
// reply was lost. It runs synchronously and must not call back into the session.
func (c *Client) Start(parent context.Context, value *pv.Peer, onChange ...func(*pv.Peer)) (*Registration, error) {
	value, err := peermodel.Normalize(value)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancelCause(parent)
	s := &Registration{client: c, value: value, id: value.Id, runtimeID: uuid.NewString(), token: uuid.NewString(), ctx: ctx, cancel: cancel, done: make(chan struct{})}
	if len(onChange) > 0 {
		s.onChange = onChange[0]
	}
	rpcCtx, stop := context.WithTimeout(ctx, 5*time.Second)
	entry, err := c.Lookup(rpcCtx, value.Id)
	revision := ""
	if err == nil {
		revision = entry.Revision
	} else if connect.CodeOf(err) != connect.CodeNotFound {
		stop()
		cancel(err)
		return nil, err
	}
	response, err := c.Register(rpcCtx, &rv.RegisterRequest{Peer: value, RuntimeId: s.runtimeID, Token: s.token, ExpectedRevision: revision})
	stop()
	if err != nil {
		cancel(err)
		s.disconnect()
		return nil, err
	}
	s.accept(response.Entry)
	timeout := response.HeartbeatTimeout.AsDuration()
	if timeout < time.Second {
		cancel(errors.New("discovery: invalid heartbeat policy"))
		s.disconnect()
		return nil, context.Cause(ctx)
	}
	s.close = sync.OnceFunc(func() {
		cancel(context.Canceled)
		<-s.done
		s.disconnect()
	})
	go s.maintain(timeout)
	return s, nil
}
func (s *Registration) maintain(timeout time.Duration) {
	defer close(s.done)
	ticker := time.NewTicker(timeout / 3)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.mu.Lock()
			ctx, stop := context.WithTimeout(s.ctx, 5*time.Second)
			response, err := s.client.Heartbeat(ctx, &rv.HeartbeatRequest{Id: s.value.Id, RuntimeId: s.runtimeID, Token: s.token})
			if connect.CodeOf(err) == connect.CodeFailedPrecondition {
				entry, lookupErr := s.client.Lookup(ctx, s.value.Id)
				err = lookupErr
				if err == nil {
					if entry.Presence.RuntimeId != s.runtimeID {
						err = connect.NewError(connect.CodePermissionDenied, "discovery: runtime ownership was replaced")
					} else if entry.Presence.State == rv.PresenceState_PRESENCE_STATE_OFFLINE {
						err = connect.NewError(connect.CodeFailedPrecondition, "discovery: runtime disconnected")
					} else {
						registered, registerErr := s.client.Register(ctx, &rv.RegisterRequest{Peer: entry.Peer, RuntimeId: s.runtimeID, Token: s.token, ExpectedRevision: entry.Revision})
						err = registerErr
						if err == nil {
							s.accept(registered.Entry)
							timeout = registered.HeartbeatTimeout.AsDuration()
						}
					}
				}
			} else if err == nil {
				s.accept(response.Entry)
				timeout = response.HeartbeatTimeout.AsDuration()
			}
			stop()
			s.mu.Unlock()
			if err != nil {
				switch connect.CodeOf(err) {
				case connect.CodeUnavailable, connect.CodeDeadlineExceeded, connect.CodeCanceled, connect.CodeAborted:
					// A temporary outage changes observed presence, not agent identity.
				default:
					s.cancel(err)
					return
				}
			} else if timeout >= time.Second {
				ticker.Reset(timeout / 3)
			} else {
				s.cancel(errors.New("discovery: invalid heartbeat policy"))
				return
			}
		}
	}
}
func (s *Registration) Update(ctx context.Context, value *pv.Peer) error {
	value, err := peermodel.Normalize(value)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if value.Id != s.value.Id {
		return errors.New("discovery: metadata update must preserve peer identity")
	}
	if err := s.ctx.Err(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	response, err := s.client.Register(ctx, &rv.RegisterRequest{Peer: value, RuntimeId: s.runtimeID, Token: s.token, ExpectedRevision: s.revision})
	if err != nil {
		return err
	}
	s.accept(response.Entry)
	return nil
}

func (s *Registration) disconnect() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = s.client.Disconnect(ctx, &rv.DisconnectRequest{Id: s.id, RuntimeId: s.runtimeID, Token: s.token})
}

// accept reconciles the canonical snapshot after a successful operation. A lost
// metadata acknowledgement can be resolved by the next heartbeat without
// replaying an older local snapshot during presence recovery.
func (s *Registration) accept(entry *rv.Entry) {
	changed := !proto.Equal(s.value, entry.Peer)
	s.value = proto.Clone(entry.Peer).(*pv.Peer)
	s.revision = entry.Revision
	if changed && s.onChange != nil {
		s.onChange(proto.Clone(s.value).(*pv.Peer))
	}
}
