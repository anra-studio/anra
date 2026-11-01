package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"time"

	"buf.build/go/protovalidate"
	pb "github.com/anra-studio/anra/gen/anra/a2a/tape/v1"
	"github.com/anra-studio/anra/pkg/model/anchor"
	bolt "go.etcd.io/bbolt"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

var (
	rolesBucket  = []byte("anra.mcp.roles.v1")
	grantsBucket = []byte("anra.mcp.grants.v1")
	sourceBucket = []byte("anra.mcp.source.v1")
)

// stateStore persists role revisions and source identity alongside revocable
// grants. Handoffs and their fixed windows live only on Tape. Only token hashes
// are stored; the credential is returned once.
type stateStore struct {
	db     *bolt.DB
	source *pb.Source
}

var _ StateStore = (*stateStore)(nil)

// OpenState opens the bundled bbolt implementation for caller-managed storage.
// The caller closes it after all services using it have stopped. Its existing
// source binding must match source; role and credential records keep their
// original on-disk representation.
func OpenState(path string, source *pb.Source) (StateStore, error) {
	input := struct {
		Path   string     `validate:"required"`
		Source *pb.Source `validate:"required,structonly"`
	}{path, source}
	if validate.Struct(input) != nil || protovalidate.Validate(source) != nil {
		return nil, ErrInvalidReference
	}
	return openState(path, source)
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
	return &stateStore{db: db, source: proto.Clone(source).(*pb.Source)}, nil
}

func roleKey(r RoleRef) string {
	data, _ := json.Marshal([]string{r.ID, r.Version})
	return string(data)
}

func get(tx *bolt.Tx, bucket []byte, key string, out any) error {
	b := tx.Bucket(bucket)
	if b == nil {
		return ErrStateNotFound
	}
	data := b.Get([]byte(key))
	if data == nil {
		return ErrStateNotFound
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
		current, err := json.Marshal(value)
		if err != nil {
			return err
		}
		b, err := tx.CreateBucketIfNotExists(bucket)
		if err != nil {
			return err
		}
		if previous := b.Get([]byte(key)); previous != nil {
			if bytes.Equal(current, previous) {
				return nil
			}
			return ErrFailedPrecondition
		}
		return b.Put([]byte(key), current)
	})
}

func (s *stateStore) Source() *pb.Source { return proto.Clone(s.source).(*pb.Source) }

func (s *stateStore) Close() error { return s.db.Close() }

func (s *stateStore) SaveRole(ctx context.Context, role RoleDefinition) error {
	return s.insert(ctx, rolesBucket, roleKey(role.RoleRef), role)
}

func (s *stateStore) Role(ctx context.Context, ref RoleRef) (RoleDefinition, error) {
	var role RoleDefinition
	err := s.load(ctx, rolesBucket, roleKey(ref), &role)
	return role, err
}

func (s *stateStore) SaveGrant(ctx context.Context, digest string, grant Grant) error {
	return s.insert(ctx, grantsBucket, digest, GrantRecord{Grant: grant})
}

func (s *stateStore) Grant(ctx context.Context, digest string) (GrantRecord, error) {
	var record GrantRecord
	err := s.load(ctx, grantsBucket, digest, &record)
	return record, err
}

func (s *stateStore) RevokeGrant(ctx context.Context, digest string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		var g GrantRecord
		if err := get(tx, grantsBucket, digest, &g); err != nil {
			return err
		}
		g.Revoked = true
		return put(tx, grantsBucket, digest, g)
	})
}
