package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync"

	rpcpb "github.com/anra-studio/anra/gen/anra/a2a/tape/v1"
	"github.com/anra-studio/anra/pkg/a2a"
	"github.com/anra-studio/anra/pkg/model/view"
	"github.com/cucumber/godog"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	"github.com/scbizu/tape-go/pkg/tape/storage"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
	bolt "go.etcd.io/bbolt"
)

// readLog observes actual disk-backed Range calls made through the HTTP API.
// It does not replace storage behavior or the content exporter.
type readLog struct {
	storage.TapeStorage
	mu     sync.Mutex
	ranges []tv.EntryRange
}

func (r *readLog) Range(ctx context.Context, scope tv.EntryRange, opts ...storage.RangeBy) (tv.EntryView, error) {
	r.mu.Lock()
	r.ranges = append(r.ranges, scope)
	r.mu.Unlock()
	return r.TapeStorage.Range(ctx, scope, opts...)
}

func (r *readLog) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ranges = nil
}

func (p *peerContentScenario) checkReadRanges() error {
	p.reads.mu.Lock()
	ranges := slices.Clone(p.reads.ranges)
	p.reads.mu.Unlock()
	seqs := []te.Seq{
		te.MustParseSeq(p.ref.Scopes[0].SeqS),
		te.MustParseSeq(p.ref.Scopes[0].SeqS).Next(),
		te.MustParseSeq(p.ref.Scopes[1].SeqS),
		te.MustParseSeq(p.ref.Scopes[1].SeqS).Next(),
	}
	if len(ranges) != len(seqs) {
		return fmt.Errorf("complete read performed %d storage reads, want %d", len(ranges), len(seqs))
	}
	for i, scope := range ranges {
		if scope.SeqS != seqs[i] || scope.SeqE != seqs[i].Next() {
			return fmt.Errorf("complete read range [%s,%s), want only entry %s", scope.SeqS, scope.SeqE, seqs[i])
		}
	}
	return nil
}

func (p *peerContentScenario) checkLazyReader() error {
	p.reads.reset()
	ctx, cancel := context.WithCancel(p.s.ctx)
	defer cancel()
	stream, err := p.s.service.Read(ctx, "borrower", p.s.decision.Lease.Id)
	if err != nil {
		return err
	}
	p.reads.mu.Lock()
	before := len(p.reads.ranges)
	p.reads.mu.Unlock()
	if before != 0 {
		return errors.New("opening a reader accessed context storage")
	}
	prefix := make([]byte, 1)
	n, err := stream.Read(prefix)
	if err != nil || n != 1 || prefix[0] != '{' {
		return fmt.Errorf("initial reader bytes = %q, error %v", prefix, err)
	}
	cancel()
	if _, err := stream.Read(prefix); !errors.Is(err, context.Canceled) {
		return fmt.Errorf("canceled reader returned %v", err)
	}
	p.reads.mu.Lock()
	after := len(p.reads.ranges)
	p.reads.mu.Unlock()
	if after != 0 {
		return errors.New("reading only metadata or canceling accessed context storage")
	}
	return nil
}

func initializeBorrowRead(sc *godog.ScenarioContext, s *scenario) {
	sc.Step(`^授权视图后一段的正文丢失并重启存储$`, s.loseLastFinding)
	sc.Step(
		`^读取缺失的授权视图失败且不返回部分内容$`,
		func() error {
			response, err := s.client.Read(s.ctx, &rpcpb.ReadRequest{
				LeaseId: s.decision.Lease.Id,
			})
			if !errors.Is(err, a2a.ErrStorage) || response != nil {
				return fmt.Errorf("incomplete view returned %v, error %v", response, err)
			}
			stream, err := s.service.Read(s.ctx, "borrower", s.decision.Lease.Id)
			if err != nil {
				return err
			}
			quote, err := view.Decode(stream)
			if !errors.Is(err, a2a.ErrStorage) || quote != nil {
				return fmt.Errorf("incomplete stream returned %v, error %v", quote, err)
			}
			return nil
		},
	)
}

// Remove a real persisted content record while retaining its committed View
// and lease. Recovery must not silently skip that hole during an authorized read.
func (s *scenario) loseLastFinding() error {
	seq := te.MustParseSeq(s.decision.Lease.Ref.Scopes[1].SeqS)
	if err := s.stopPeer(); err != nil {
		return err
	}
	db, err := bolt.Open(filepath.Join(s.root, "researcher.db"), 0o600, nil)
	if err != nil {
		return err
	}
	err = db.Update(
		func(tx *bolt.Tx) error {
			bucket := tx.Bucket([]byte("entries")).Bucket([]byte("researcher-local")).Bucket([]byte("session"))
			cursor := bucket.Cursor()
			for key, value := cursor.First(); key != nil; key, value = cursor.Next() {
				var raw struct {
					Seq te.Seq
				}
				if err := json.Unmarshal(value, &raw); err != nil {
					return err
				}
				if raw.Seq == seq {
					return cursor.Delete()
				}
			}
			return fmt.Errorf("fixture entry %s was not persisted", seq)
		},
	)
	if err := errors.Join(err, db.Close()); err != nil {
		return err
	}
	s.peerTape, err = s.openTape("researcher", "researcher-local")
	if err != nil {
		return err
	}
	return s.startPeer()
}
