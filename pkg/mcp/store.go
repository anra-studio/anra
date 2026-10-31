package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	pb "github.com/anra-studio/anra/gen/anra/a2a/tape/v1"
	"github.com/anra-studio/anra/pkg/model/anchor"
	bolt "go.etcd.io/bbolt"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

var errMissing = errors.New("missing record")

var (
	rolesBucket  = []byte("anra.mcp.roles.v1")
	grantsBucket = []byte("anra.mcp.grants.v1")
	sourceBucket = []byte("anra.mcp.source.v1")
)

// stateStore persists role revisions and source identity alongside revocable
// grants. Handoffs and their fixed windows live only on Tape. Only token hashes
// are stored; the credential is returned once.
type stateStore struct{ db *bolt.DB }

type storedGrant struct {
	Grant   Grant `json:"grant"`
	Revoked bool  `json:"revoked"`
}

func openState(path string, source *pb.Source) (*stateStore, error) {
	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, err
	}
	err = db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(sourceBucket)
		if err != nil {
			return err
		}
		data := b.Get([]byte("source"))
		if data != nil {
			var previous pb.Source
			if protojson.Unmarshal(data, &previous) != nil || !proto.Equal(&previous, source) {
				return ErrFailedPrecondition
			}
			return nil
		}
		return b.Put([]byte("source"), anchor.ProtoJSON(source))
	})
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return &stateStore{db}, nil
}

func roleKey(r RoleRef) string {
	data, _ := json.Marshal([]string{r.ID, r.Version})
	return string(data)
}

func tokenHash(token string) string {
	d := sha256.Sum256([]byte(token))
	return hex.EncodeToString(d[:])
}

func get(tx *bolt.Tx, bucket []byte, key string, out any) error {
	b := tx.Bucket(bucket)
	if b == nil {
		return errMissing
	}
	data := b.Get([]byte(key))
	if data == nil {
		return errMissing
	}
	return decode(data, out)
}

func put(tx *bolt.Tx, bucket []byte, key string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	b, err := tx.CreateBucketIfNotExists(bucket)
	if err != nil {
		return err
	}
	return b.Put([]byte(key), data)
}

func (s *stateStore) load(ctx context.Context, bucket []byte, key string, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.db.View(func(tx *bolt.Tx) error { return get(tx, bucket, key, out) })
}

func (s *stateStore) insert(ctx context.Context, bucket []byte, key string, value any) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		var previous json.RawMessage
		err := get(tx, bucket, key, &previous)
		if err == nil {
			current, err := json.Marshal(value)
			if err != nil {
				return err
			}
			if string(current) == string(previous) {
				return nil
			}
			return ErrFailedPrecondition
		}
		if !errors.Is(err, errMissing) {
			return err
		}
		return put(tx, bucket, key, value)
	})
}

func (s *stateStore) revoke(ctx context.Context, token string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		var g storedGrant
		if err := get(tx, grantsBucket, tokenHash(token), &g); err != nil {
			if errors.Is(err, errMissing) {
				return ErrPermissionDenied
			}
			return ErrUnavailable
		}
		g.Revoked = true
		return put(tx, grantsBucket, tokenHash(token), g)
	})
}
