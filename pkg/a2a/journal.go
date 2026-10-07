package a2a

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"

	bolt "go.etcd.io/bbolt"
)

var (
	grantBucket   = []byte("anra:grant-journal/v2")
	requestBucket = []byte("requests")
	viewBucket    = []byte("views")
	historyBucket = []byte("history")
	sourceKey     = []byte("source")
)

func key(p, id string) string { b, _ := json.Marshal([]string{p, id}); return string(b) }

// journalBuckets touches only metadata. There is no startup replay or scan.
func (s *Service) journalBuckets(tx *bolt.Tx, create bool) (*bolt.Bucket, error) {
	root := tx.Bucket(grantBucket)
	if root == nil {
		// Older tape journals require explicit migration; never silently discard
		// their decisions or revocations by treating them as an empty database.
		if tx.Bucket([]byte("entries")) != nil {
			return nil, ErrStorage
		}
		if !create {
			return nil, nil
		}
		var err error
		root, err = tx.CreateBucket(grantBucket)
		if err != nil {
			return nil, err
		}
	}
	b := root.Bucket([]byte(s.config.JournalOwner))
	if b == nil {
		if !create {
			return nil, nil
		}
		var err error
		b, err = root.CreateBucket([]byte(s.config.JournalOwner))
		if err != nil {
			return nil, err
		}
		data, err := json.Marshal(s.config.Source)
		if err != nil {
			return nil, err
		}
		if err := b.Put(sourceKey, data); err != nil {
			return nil, err
		}
		for _, name := range [][]byte{requestBucket, viewBucket, historyBucket} {
			if _, err := b.CreateBucket(name); err != nil {
				return nil, err
			}
		}
	}
	var source Source
	if decode(b.Get(sourceKey), &source) != nil || source != s.config.Source {
		return nil, ErrStorage
	}
	for _, name := range [][]byte{requestBucket, viewBucket, historyBucket} {
		if b.Bucket(name) == nil {
			return nil, ErrStorage
		}
	}
	return b, nil
}

func (s *Service) validateRecord(r grantRecord) error {
	if r.Schema != journalKind || r.Source != s.config.Source || r.Requester == "" || r.Request.validate() != nil || r.Decision.RequestID != r.Request.RequestID {
		return ErrStorage
	}
	switch r.Decision.Status {
	case Granted:
		v := r.Decision.View
		if v == nil || v.ID == "" || v.Source != r.Source || len(v.Ranges) > 1 || v.ExpiresAt.IsZero() || r.Codec == "" || r.HeadHash == "" || r.EntryHash == "" {
			return ErrStorage
		}
		for _, b := range v.Ranges {
			if !validRange(b, v.SnapshotHead) {
				return ErrStorage
			}
		}
	case Denied, Pending:
		if r.Decision.View != nil || r.Closed {
			return ErrStorage
		}
	default:
		return ErrStorage
	}
	return nil
}

func (s *Service) loadRecord(b *bolt.Bucket, k []byte) (grantRecord, bool, error) {
	var r grantRecord
	if b == nil {
		return r, false, nil
	}
	data := b.Bucket(requestBucket).Get(k)
	if data == nil {
		return r, false, nil
	}
	if err := decode(data, &r); err != nil {
		return r, false, err
	}
	if err := s.validateRecord(r); err != nil {
		return r, false, err
	}
	if !bytes.Equal(k, []byte(key(r.Requester, r.Request.RequestID))) {
		return r, false, ErrStorage
	}
	if r.Decision.View != nil && !bytes.Equal(b.Bucket(viewBucket).Get([]byte(r.Decision.View.ID)), k) {
		return r, false, ErrStorage
	}
	return r, true, nil
}

func (s *Service) requestGrant(ctx context.Context, p, requestID string) (r grantRecord, found bool, err error) {
	if err = ctx.Err(); err != nil {
		return
	}
	err = s.config.Journal.View(func(tx *bolt.Tx) error {
		b, err := s.journalBuckets(tx, false)
		if err != nil {
			return err
		}
		r, found, err = s.loadRecord(b, []byte(key(p, requestID)))
		return err
	})
	if err != nil {
		err = storageError(err)
	}
	return
}

func (s *Service) viewGrant(ctx context.Context, p, vid string) (r grantRecord, found bool, err error) {
	if err = ctx.Err(); err != nil {
		return
	}
	err = s.config.Journal.View(func(tx *bolt.Tx) error {
		b, err := s.journalBuckets(tx, false)
		if err != nil || b == nil {
			return err
		}
		k := b.Bucket(viewBucket).Get([]byte(vid))
		if k == nil {
			return nil
		}
		r, found, err = s.loadRecord(b, k)
		if err != nil {
			return err
		}
		if !found || r.Decision.View == nil || r.Decision.View.ID != vid {
			return ErrStorage
		}
		found = r.Requester == p
		return nil
	})
	if err != nil {
		err = storageError(err)
	}
	return
}

// append commits history, current state and the view index atomically. Comparing
// previous prevents a stale policy decision from overwriting another service's
// committed grant or closure. No derived in-memory projection is needed.
func (s *Service) append(ctx context.Context, r grantRecord, previous *grantRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.validateRecord(r); err != nil {
		return err
	}
	data, err := json.Marshal(r)
	if err != nil {
		return storageError(err)
	}
	err = s.config.Journal.Update(func(tx *bolt.Tx) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		b, err := s.journalBuckets(tx, true)
		if err != nil {
			return err
		}
		k := []byte(key(r.Requester, r.Request.RequestID))
		current, found, err := s.loadRecord(b, k)
		if err != nil {
			return err
		}
		if (previous == nil && found) || (previous != nil && (!found || hash(current) != hash(*previous))) {
			return ErrConflict
		}
		if found {
			unclosed := r
			unclosed.Closed = false
			if hash(current.Request) != hash(r.Request) || current.Closed || (current.Decision.Status != Pending && (hash(current) != hash(unclosed) || !r.Closed)) {
				return ErrConflict
			}
		} else if r.Closed {
			return ErrConflict
		}
		views := b.Bucket(viewBucket)
		if r.Decision.View != nil {
			vid := []byte(r.Decision.View.ID)
			if prior := views.Get(vid); prior != nil && !bytes.Equal(prior, k) {
				return ErrConflict
			}
			if err := views.Put(vid, k); err != nil {
				return err
			}
		}
		if err := b.Bucket(requestBucket).Put(k, data); err != nil {
			return err
		}
		history := b.Bucket(historyBucket)
		seq, err := history.NextSequence()
		if err != nil {
			return err
		}
		if seq == 0 {
			return ErrStorage
		}
		var id [8]byte
		binary.BigEndian.PutUint64(id[:], seq)
		if history.Get(id[:]) != nil {
			return ErrStorage
		}
		return history.Put(id[:], data)
	})
	if err == ErrConflict {
		return err
	}
	if err != nil {
		return storageError(err)
	}
	return nil
}
