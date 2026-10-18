// Package journal stores tape-view grants in one bolthold (bbolt) database
// file.
//
// The journal keeps one current state per request plus an append-only audit
// trail. Resolving a pending request or releasing a grant appends a new record;
// a granted record is never rewritten, so the trail is the only trace of an
// earlier decision. Record types are stored and indexed by bolthold.
package journal

import (
	"buf.build/go/protovalidate"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	pb "github.com/scbizu/anra/gen/anra/a2a/tape/v1"
	"github.com/timshannon/bolthold"
	bolt "go.etcd.io/bbolt"
	"google.golang.org/protobuf/proto"
)

// Errors returned by this package. Use errors.Is to test for them.
var (
	// ErrStorage reports a corrupt journal, a journal bound to another source
	// tape, or a failed transaction.
	ErrStorage = errors.New("grant journal failure")
	// ErrConflict reports a concurrent change to the record being written.
	ErrConflict = errors.New("grant journal conflict")
)

// Record kinds. A grant is written once; a request state exists only until it
// is resolved.
const (
	grantKind   = "grant"
	requestKind = "request"
	releaseKind = "release"
)

// The source binding key, the bucket name bolthold derives from the record type
// and the indexed field. All three are read before trusting a file.
const (
	sourceKey          = "source"
	requestStateBucket = "requestState"
	viewIDIndex        = "ViewID"
)

// foreignBuckets hold another journal layout in the same file. Such a file must
// never be read as an empty journal: its decisions and revocations need
// explicit migration.
var foreignBuckets = [][]byte{[]byte("entries"), []byte("anra:grant-journal/v2")}

// State is a request's current outcome.
type State struct {
	Requester string
	Request   *pb.BorrowRequest
	Decision  *pb.Decision
	Released  bool
}

func sameState(a, b State) bool {
	return a.Requester == b.Requester && a.Released == b.Released && proto.Equal(a.Request, b.Request) && proto.Equal(a.Decision, b.Decision)
}

// key namespaces a request ID by requester, so requesters can reuse IDs.
func key(requester, requestID string) string {
	b, _ := json.Marshal([]string{requester, requestID})
	return string(b)
}

// Store is one source tape's grant journal in its own database file. Every
// Service that borrows from that tape shares one Store; the caller owns it and
// closes it with Close.
type Store struct {
	hold   *bolthold.Store
	source *pb.Source
}

// Open opens or creates the journal file and binds it to one source tape. Only
// the file itself is opened: no bucket is created and no record is read until
// the first operation.
func Open(path string, source *pb.Source) (*Store, error) {
	if path == "" || source == nil {
		return nil, fmt.Errorf("%w: a journal needs a file and a source tape", ErrStorage)
	}
	hold, err := bolthold.Open(path, 0600, &bolthold.Options{
		Encoder: json.Marshal,
		Decoder: decode,
		Options: &bolt.Options{Timeout: time.Second},
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrStorage, err)
	}
	return &Store{hold: hold, source: proto.Clone(source).(*pb.Source)}, nil
}

// Source is the tape identity this journal is bound to.
func (s *Store) Source() *pb.Source { return proto.Clone(s.source).(*pb.Source) }

// Close closes the journal file.
func (s *Store) Close() error { return s.hold.Close() }

// check verifies this file is a journal of this source tape and returns its
// binding. A file holding records without a binding, or another journal layout,
// fails closed instead of being read as empty. The returned binding carries this
// source tape, so the first write can persist it.
func (s *Store) check(tx *bolt.Tx) (sourceBinding, error) {
	for _, name := range foreignBuckets {
		if tx.Bucket(name) != nil {
			return sourceBinding{}, ErrStorage
		}
	}
	var binding sourceBinding
	err := s.hold.TxGet(tx, sourceKey, &binding)
	switch {
	case err == nil:
		if !proto.Equal(binding.Source.message(), s.source) {
			return binding, ErrStorage
		}
		return binding, nil
	case !errors.Is(err, bolthold.ErrNotFound):
		return binding, err
	case tx.Bucket([]byte(requestStateBucket)) != nil:
		return binding, ErrStorage
	}
	return sourceBinding{Source: storeSource(s.source)}, nil
}

// load returns the current outcome stored under k, including its release.
func (s *Store) load(tx *bolt.Tx, k string) (State, bool, error) {
	var rec requestState
	err := s.hold.TxGet(tx, k, &rec)
	if errors.Is(err, bolthold.ErrNotFound) {
		return State{}, false, nil
	}
	if err != nil {
		return State{}, false, err
	}
	st, err := s.state(rec)
	if err != nil {
		return State{}, false, err
	}
	if st.Decision.View == nil {
		return st, true, nil
	}
	var release grantRelease
	err = s.hold.TxGet(tx, st.Decision.View.ViewId, &release)
	switch {
	case err == nil:
		if release.ViewID != st.Decision.View.ViewId {
			return State{}, false, ErrStorage
		}
		st.Released = true
	case !errors.Is(err, bolthold.ErrNotFound):
		return State{}, false, err
	}
	return st, true, nil
}

// state decodes a record, failing closed on a shape this package never writes.
func (s *Store) state(rec requestState) (State, error) {
	if rec.Requester == "" || key(rec.Requester, rec.RequestID) != rec.Key {
		return State{}, ErrStorage
	}
	request := rec.Request.message()
	if err := validateRecord(request); err != nil {
		return State{}, err
	}
	st := State{Requester: rec.Requester, Request: request, Decision: &pb.Decision{RequestId: rec.RequestID}}
	switch rec.Kind {
	case grantKind:
		if rec.View == nil || rec.ViewID == "" {
			return State{}, ErrStorage
		}
		view := rec.View.message(rec.ViewID)
		if validateRecord(view) != nil || !proto.Equal(view.Source, s.source) {
			return State{}, ErrStorage
		}
		st.Decision.Status = pb.DecisionStatus_DECISION_STATUS_GRANTED
		st.Decision.View = view
	case requestKind:
		status, ok := rec.Status.status()
		if !ok || rec.View != nil || rec.ViewID != "" {
			return State{}, ErrStorage
		}
		st.Decision.Status = status
	default:
		return State{}, ErrStorage
	}
	return st, nil
}

// RequestGrant loads the current outcome of one request.
func (s *Store) RequestGrant(ctx context.Context, requester, requestID string) (State, bool, error) {
	var st State
	if err := ctx.Err(); err != nil {
		return st, false, err
	}
	var found bool
	err := s.hold.Bolt().View(func(tx *bolt.Tx) error {
		if _, err := s.check(tx); err != nil {
			return err
		}
		var err error
		st, found, err = s.load(tx, key(requester, requestID))
		return err
	})
	return st, found, failure(err)
}

// ViewGrant loads the grant that issued viewID, and reports whether requester
// holds it. The query uses the view index, then resolves the record by its own
// key so a stale index entry cannot serve another request's grant.
func (s *Store) ViewGrant(ctx context.Context, requester, viewID string) (State, bool, error) {
	var st State
	if err := ctx.Err(); err != nil {
		return st, false, err
	}
	found := false
	err := s.hold.Bolt().View(func(tx *bolt.Tx) error {
		if _, err := s.check(tx); err != nil {
			return err
		}
		var rec requestState
		err := s.hold.TxFindOne(tx, &rec, bolthold.Where(viewIDIndex).Eq(viewID).Index(viewIDIndex))
		if errors.Is(err, bolthold.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		st, found, err = s.load(tx, rec.Key)
		if err == nil && (!found || st.Decision.View == nil || st.Decision.View.ViewId != viewID) {
			return ErrStorage
		}
		found = found && st.Requester == requester
		return err
	})
	return st, found, failure(err)
}

// Decide records a request's outcome: a grant, or a pending or denied state.
// previous is the pending state being resolved, or nil for a new request. It is
// compared inside the transaction, so a stale decision cannot overwrite another
// writer's committed outcome.
func (s *Store) Decide(ctx context.Context, requester string, req *pb.BorrowRequest, d *pb.Decision, previous *State) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	rec, kind, err := s.record(requester, req, d)
	if err != nil {
		return err
	}
	k := key(requester, req.GetRequestId())
	err = s.hold.Bolt().Update(func(tx *bolt.Tx) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		binding, err := s.check(tx)
		if err != nil {
			return err
		}
		current, found, err := s.load(tx, k)
		if err != nil {
			return err
		}
		switch {
		case !found && previous != nil, found && (previous == nil || !sameState(current, *previous)):
			return ErrConflict
		case found && (current.Decision.Status != pb.DecisionStatus_DECISION_STATUS_PENDING || !proto.Equal(current.Request, req)):
			// Only a pending request may be decided again.
			return ErrConflict
		}
		if rec.ViewID != "" {
			var prior requestState
			err := s.hold.TxFindOne(tx, &prior, bolthold.Where(viewIDIndex).Eq(rec.ViewID).Index(viewIDIndex))
			if err == nil && prior.Key != k {
				return ErrConflict
			}
			if err != nil && !errors.Is(err, bolthold.ErrNotFound) {
				return err
			}
		}
		if err := s.hold.TxUpsert(tx, k, rec); err != nil {
			return err
		}
		return s.commit(tx, &binding, kind, rec)
	})
	return failure(err)
}

// Release ends the grant held by st. A grant that changed, or was already
// released, since st was loaded is a conflict.
func (s *Store) Release(ctx context.Context, st State) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	view := st.Decision.GetView()
	if view == nil || view.ViewId == "" {
		return ErrStorage
	}
	release := grantRelease{ViewID: view.ViewId, Requester: st.Requester}
	err := s.hold.Bolt().Update(func(tx *bolt.Tx) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		binding, err := s.check(tx)
		if err != nil {
			return err
		}
		current, found, err := s.load(tx, key(st.Requester, st.Request.GetRequestId()))
		if err != nil {
			return err
		}
		if !found || !sameState(current, st) || current.Released {
			return ErrConflict
		}
		if err := s.hold.TxInsert(tx, view.ViewId, release); err != nil {
			return err
		}
		return s.commit(tx, &binding, releaseKind, release)
	})
	return failure(err)
}

// record is a decision's stored form.
func (s *Store) record(requester string, req *pb.BorrowRequest, d *pb.Decision) (requestState, string, error) {
	request, err := storeRequest(req)
	if err != nil {
		return requestState{}, "", err
	}
	rec := requestState{Key: key(requester, req.GetRequestId()), RequestID: req.GetRequestId(), Requester: requester, Request: request}
	if d.GetStatus() != pb.DecisionStatus_DECISION_STATUS_GRANTED {
		status, ok := storeStatus(d.GetStatus())
		if !ok {
			return requestState{}, "", ErrStorage
		}
		rec.Kind, rec.Status = requestKind, status
		return rec, requestKind, nil
	}
	view := d.GetView()
	if view == nil || validateRecord(view) != nil || !proto.Equal(view.Source, s.source) {
		return requestState{}, "", ErrStorage
	}
	stored, err := storeView(view)
	if err != nil {
		return requestState{}, "", err
	}
	rec.Kind, rec.ViewID, rec.View = grantKind, view.ViewId, &stored
	return rec, grantKind, nil
}

// commit appends the audit record for one write and stores the binding with its
// advanced audit sequence.
func (s *Store) commit(tx *bolt.Tx, binding *sourceBinding, kind string, record any) error {
	binding.Audit++
	if err := s.audit(tx, binding.Audit, kind, record); err != nil {
		return err
	}
	return s.hold.TxUpsert(tx, sourceKey, *binding)
}

// audit appends to the trail. The body is the record's own JSON, the same form
// bolthold stores, and the key keeps append order (see auditKey).
func (s *Store) audit(tx *bolt.Tx, seq uint64, kind string, record any) error {
	body, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrStorage, err)
	}
	return s.hold.TxInsert(tx, auditKey(seq), auditEntry{Seq: seq, Kind: kind, Body: body})
}

// auditKey orders the trail by append sequence. The zero padding keeps key order
// the same as sequence order.
func auditKey(seq uint64) string { return fmt.Sprintf("%020d", seq) }

// validateRecord rejects a message that no longer satisfies the proto schema,
// so a corrupt journal cannot project state the service never wrote.
func validateRecord(value proto.Message) error {
	if value == nil || !value.ProtoReflect().IsValid() {
		return ErrStorage
	}
	if err := protovalidate.Validate(value); err != nil {
		return fmt.Errorf("%w: %v", ErrStorage, err)
	}
	return nil
}

// failure wraps transaction errors as ErrStorage while passing through the
// errors this package raises itself and caller cancellation.
func failure(err error) error {
	if err == nil || errors.Is(err, ErrConflict) || errors.Is(err, ErrStorage) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("%w: %v", ErrStorage, err)
}
