// Package registry owns durable Peer metadata and independently observed presence.
package registry

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	pv "github.com/anra-studio/anra/gen/anra/peer/v1"
	rv "github.com/anra-studio/anra/gen/anra/registry/v1"
	peermodel "github.com/anra-studio/anra/pkg/model/peer"
	bolt "go.etcd.io/bbolt"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const MaxMessageBytes = 1 << 20

// Config configures a single durable registry. Now permits controlled-clock
// acceptance scenarios; production defaults to time.Now.
type Config struct {
	Path             string
	HeartbeatTimeout time.Duration
	Now              func() time.Time
}

type storedPeer struct {
	Peer           *pv.Peer
	RuntimeID      string
	TokenHash      [32]byte
	Version        uint64
	Closed         bool
	Retired        bool
	Revoked        map[string]bool
	RegisterHash   [32]byte
	RegisterEpoch  string
	ForgetRevision string
}

type observation struct {
	state rv.PresenceState
	seen  time.Time
}

type Registry struct {
	mu           sync.Mutex
	db           *bolt.DB
	epoch        string
	timeout      time.Duration
	now          func() time.Time
	observations map[string]observation
}

var peersBucket = []byte("peers-v1")

func Open(config Config) (*Registry, error) {
	if config.Path == "" {
		return nil, errors.New("registry: storage path is required")
	}
	if config.HeartbeatTimeout < time.Second {
		return nil, errors.New("registry: heartbeat timeout of at least 1s is required")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if err := os.MkdirAll(filepath.Dir(config.Path), 0o700); err != nil {
		return nil, err
	}
	db, err := bolt.Open(config.Path, 0o600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, err
	}
	if err := db.Update(func(tx *bolt.Tx) error { _, err := tx.CreateBucketIfNotExists(peersBucket); return err }); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return &Registry{db: db, epoch: uuid.NewString(), timeout: config.HeartbeatTimeout, now: config.Now, observations: make(map[string]observation)}, nil
}

func (r *Registry) Close() error { r.mu.Lock(); defer r.mu.Unlock(); return r.db.Close() }

func failure(code connect.Code, text string) error { return connect.NewError(code, text) }
func validateRequest(ctx context.Context, request proto.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := protovalidate.Validate(request); err != nil {
		return failure(connect.CodeInvalidArgument, "registry: invalid request: "+err.Error())
	}
	if proto.Size(request) > MaxMessageBytes {
		return failure(connect.CodeResourceExhausted, "registry: request exceeds 1 MiB")
	}
	return nil
}
func (r *Registry) revision(record *storedPeer) string {
	return fmt.Sprintf("%s/%d", r.epoch, record.Version)
}
func (r *Registry) read(id string) (*storedPeer, error) {
	var record *storedPeer
	err := r.db.View(func(tx *bolt.Tx) error {
		data := tx.Bucket(peersBucket).Get([]byte(id))
		if data == nil {
			return nil
		}
		record = &storedPeer{}
		return json.Unmarshal(data, record)
	})
	if err != nil {
		return nil, failure(connect.CodeUnavailable, "registry: storage read failed")
	}
	return record, nil
}
func (r *Registry) write(id string, record *storedPeer) error {
	data, err := json.Marshal(record)
	if err == nil {
		err = r.db.Update(func(tx *bolt.Tx) error { return tx.Bucket(peersBucket).Put([]byte(id), data) })
	}
	if err != nil {
		return failure(connect.CodeUnavailable, "registry: storage commit failed")
	}
	return nil
}
func (r *Registry) presence(record *storedPeer) *rv.Presence {
	observed := r.observations[record.Peer.Id]
	if observed.state == rv.PresenceState_PRESENCE_STATE_ONLINE && !r.now().Before(observed.seen.Add(r.timeout)) {
		observed.state = rv.PresenceState_PRESENCE_STATE_UNKNOWN
		r.observations[record.Peer.Id] = observed
	}
	p := &rv.Presence{RuntimeId: record.RuntimeID, State: observed.state}
	if !observed.seen.IsZero() {
		p.LastSeen = timestamppb.New(observed.seen)
	}
	return p
}
func (r *Registry) entry(record *storedPeer) *rv.Entry {
	return &rv.Entry{Peer: proto.Clone(record.Peer).(*pv.Peer), Presence: r.presence(record), Revision: r.revision(record)}
}
func (r *Registry) known(id string) (*storedPeer, error) {
	record, err := r.read(id)
	if err != nil {
		return nil, err
	}
	if record == nil || record.Retired {
		return nil, failure(connect.CodeNotFound, "registry: peer is not registered")
	}
	return record, nil
}
func owned(record *storedPeer, runtimeID, token string) error {
	if record.RuntimeID != runtimeID || record.TokenHash != sha256.Sum256([]byte(token)) {
		return failure(connect.CodePermissionDenied, "registry: runtime ownership was replaced or credentials are invalid")
	}
	return nil
}

func (r *Registry) Register(ctx context.Context, req *rv.RegisterRequest) (*rv.RegisterResponse, error) {
	if err := validateRequest(ctx, req); err != nil {
		return nil, err
	}
	value, err := peermodel.Normalize(req.Peer)
	if err != nil {
		return nil, failure(connect.CodeInvalidArgument, err.Error())
	}
	canonical := proto.Clone(req).(*rv.RegisterRequest)
	canonical.Peer = value
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(canonical)
	if err != nil {
		return nil, failure(connect.CodeInvalidArgument, "registry: invalid protobuf metadata")
	}
	digest := sha256.Sum256(data)
	token := sha256.Sum256([]byte(req.Token))
	r.mu.Lock()
	defer r.mu.Unlock()
	record, err := r.read(value.Id)
	if err != nil {
		return nil, err
	}
	if record != nil && record.Retired {
		return nil, failure(connect.CodeFailedPrecondition, "registry: peer was forgotten")
	}
	if record == nil {
		if req.ExpectedRevision != "" {
			return nil, failure(connect.CodeAborted, "registry: record revision changed")
		}
		record = &storedPeer{Revoked: make(map[string]bool)}
	} else {
		if record.Revoked[req.RuntimeId] {
			return nil, failure(connect.CodePermissionDenied, "registry: runtime ownership was replaced")
		}
		if record.RuntimeID == req.RuntimeId {
			if err := owned(record, req.RuntimeId, req.Token); err != nil {
				return nil, err
			}
			if record.Closed {
				return nil, failure(connect.CodeFailedPrecondition, "registry: runtime disconnected")
			}
			if record.RegisterEpoch == r.epoch && record.RegisterHash == digest {
				r.observations[value.Id] = observation{state: rv.PresenceState_PRESENCE_STATE_ONLINE, seen: r.now()}
				return &rv.RegisterResponse{Entry: r.entry(record), HeartbeatTimeout: durationpb.New(r.timeout)}, nil
			}
		} else if r.presence(record).State == rv.PresenceState_PRESENCE_STATE_ONLINE {
			return nil, failure(connect.CodeAlreadyExists, "registry: agent name is already registered by an online runtime")
		}
		if req.ExpectedRevision != r.revision(record) {
			return nil, failure(connect.CodeAborted, "registry: record revision changed")
		}
		if record.RuntimeID != req.RuntimeId {
			record.Revoked[record.RuntimeID] = true
		}
	}
	record.Peer = value
	record.RuntimeID = req.RuntimeId
	record.TokenHash = token
	record.Version++
	record.Closed = false
	record.RegisterHash = digest
	record.RegisterEpoch = r.epoch
	if err := r.write(value.Id, record); err != nil {
		return nil, err
	}
	r.observations[value.Id] = observation{state: rv.PresenceState_PRESENCE_STATE_ONLINE, seen: r.now()}
	return &rv.RegisterResponse{Entry: r.entry(record), HeartbeatTimeout: durationpb.New(r.timeout)}, nil
}

func (r *Registry) Heartbeat(ctx context.Context, req *rv.HeartbeatRequest) (*rv.HeartbeatResponse, error) {
	if err := validateRequest(ctx, req); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	record, err := r.known(req.Id)
	if err != nil {
		return nil, err
	}
	if err := owned(record, req.RuntimeId, req.Token); err != nil {
		return nil, err
	}
	if record.Closed || r.presence(record).State != rv.PresenceState_PRESENCE_STATE_ONLINE {
		return nil, failure(connect.CodeFailedPrecondition, "registry: presence requires registration")
	}
	r.observations[req.Id] = observation{state: rv.PresenceState_PRESENCE_STATE_ONLINE, seen: r.now()}
	return &rv.HeartbeatResponse{Entry: r.entry(record), HeartbeatTimeout: durationpb.New(r.timeout)}, nil
}
func (r *Registry) Disconnect(ctx context.Context, req *rv.DisconnectRequest) (*rv.DisconnectResponse, error) {
	if err := validateRequest(ctx, req); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	record, err := r.known(req.Id)
	if err != nil {
		return nil, err
	}
	if err := owned(record, req.RuntimeId, req.Token); err != nil {
		return nil, err
	}
	if !record.Closed {
		record.Closed = true
		record.Version++
		if err := r.write(req.Id, record); err != nil {
			return nil, err
		}
	}
	r.observations[req.Id] = observation{state: rv.PresenceState_PRESENCE_STATE_OFFLINE, seen: r.now()}
	return &rv.DisconnectResponse{}, nil
}
func (r *Registry) Lookup(ctx context.Context, req *rv.LookupRequest) (*rv.LookupResponse, error) {
	if err := validateRequest(ctx, req); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	record, err := r.known(req.Id)
	if err != nil {
		return nil, err
	}
	return &rv.LookupResponse{Entry: r.entry(record)}, nil
}
func (r *Registry) List(ctx context.Context, req *rv.ListRequest) (*rv.ListResponse, error) {
	if err := validateRequest(ctx, req); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	result := &rv.ListResponse{}
	err := r.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(peersBucket).ForEach(func(_, data []byte) error {
			var record storedPeer
			if err := json.Unmarshal(data, &record); err != nil {
				return err
			}
			if record.Retired {
				return nil
			}
			value := r.entry(&record)
			if req.State != nil && value.Presence.State != *req.State {
				return nil
			}
			if req.Skill != "" && !peermodel.HasSkill(record.Peer, req.Skill) {
				return nil
			}
			result.Entry = append(result.Entry, value)
			return nil
		})
	})
	if err != nil {
		return nil, failure(connect.CodeUnavailable, "registry: storage read failed")
	}
	return result, nil
}
func (r *Registry) Forget(ctx context.Context, req *rv.ForgetRequest) (*rv.ForgetResponse, error) {
	if err := validateRequest(ctx, req); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	record, err := r.read(req.Id)
	if err != nil {
		return nil, err
	}
	if record == nil {
		return nil, failure(connect.CodeNotFound, "registry: peer is not registered")
	}
	if record.Retired {
		if record.ForgetRevision == req.ExpectedRevision {
			return &rv.ForgetResponse{}, nil
		}
		return nil, failure(connect.CodeNotFound, "registry: peer was forgotten")
	}
	if r.presence(record).State == rv.PresenceState_PRESENCE_STATE_ONLINE {
		return nil, failure(connect.CodeFailedPrecondition, "registry: disconnect peer before forgetting")
	}
	if req.ExpectedRevision != r.revision(record) {
		return nil, failure(connect.CodeAborted, "registry: record revision changed")
	}
	tombstone := &storedPeer{Retired: true, Version: record.Version + 1, ForgetRevision: req.ExpectedRevision}
	if err := r.write(req.Id, tombstone); err != nil {
		return nil, err
	}
	delete(r.observations, req.Id)
	return &rv.ForgetResponse{}, nil
}
