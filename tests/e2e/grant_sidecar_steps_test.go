package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	pb "github.com/anra-studio/anra/gen/anra/model/view/v1"
	"github.com/anra-studio/anra/pkg/a2a"
	"github.com/anra-studio/anra/pkg/flow/borrow"
	"github.com/anra-studio/anra/pkg/flow/grant"
	"github.com/cucumber/godog"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	"github.com/scbizu/tape-go/pkg/tape/owner"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func initializeGrantSidecar(sc *godog.ScenarioContext, s *peerScenario) {
	var original []byte
	history := func() ([]byte, error) {
		ctx := owner.WithOwnerId(s.ctx, "researcher-local")
		entries, _, err := readAllEntries(ctx, s.peerTape)
		if err != nil {
			return nil, err
		}
		return json.Marshal(entries)
	}
	sc.Step(
		`^授权 sidecar 使用 "(bbolt|jsonl)" 存储$`,
		func(backend string) error {
			if backend == "bbolt" {
				return nil // The background opens bbolt by default.
			}
			s.jsonlSidecar = true
			return s.restartPeer()
		},
	)
	sc.Step(
		`^授权 sidecar 没有记录且序号为零$`,
		func() error {
			records, head, err := s.grants.decisions(s.ctx)
			if err != nil {
				return err
			}
			if len(records) != 0 || !head.IsZero() {
				return fmt.Errorf("empty sidecar has %d records at %s", len(records), head)
			}
			return nil
		},
	)
	sc.Step(
		`^保存源 Tape 的正文和序号$`,
		func() error {
			var err error
			original, err = history()
			return err
		},
	)
	sc.Step(
		`^源 Tape 的正文和序号保持不变$`,
		func() error {
			current, err := history()
			if err != nil {
				return err
			}
			if !bytes.Equal(current, original) {
				return errors.New("grant sidecar changed the source tape")
			}
			return nil
		},
	)
	sc.Step(
		`^sidecar 按顺序保留同一视图的授权和释放$`,
		func() error {
			records, head, err := s.grants.decisions(s.ctx)
			if err != nil {
				return err
			}
			if len(records) != 2 || head != te.SeqFromUint64(2) {
				return fmt.Errorf("sidecar history = %d decisions at %s, want 2 at 2", len(records), head)
			}
			for i, record := range records {
				if record.GetID() != te.SeqFromUint64(uint64(i+1)) || record.GetKind() != "anra:lease-decision/v1" {
					return errors.New("sidecar lost its position or decision kind")
				}
				var payload struct {
					Decision json.RawMessage `json:"decision"`
					Released bool            `json:"released"`
				}
				if err := json.Unmarshal([]byte(record.GetSummary()), &payload); err != nil {
					return err
				}
				var decision pb.Decision
				if err := protojson.Unmarshal(payload.Decision, &decision); err != nil {
					return err
				}
				if !proto.Equal(&decision, s.decision) || payload.Released != (i == 1) {
					return errors.New("sidecar changed the issued view or one-way release")
				}
			}
			return nil
		},
	)
	sc.Step(
		`^两个借阅服务并发批准同一请求且只有一个结果提交$`,
		func() error {
			if err := s.rememberPendingDecision(); err != nil {
				return err
			}
			_, head, err := s.grants.decisions(s.ctx)
			if err != nil {
				return err
			}
			type result struct {
				decision *pb.Decision
				err      error
			}
			results := make(chan result, 2)
			start := make(chan struct{})
			var writers sync.WaitGroup
			for _, service := range []*borrow.Service{s.service, s.pendingWriter} {
				writers.Go(
					func() {
						<-start
						d, err := service.Approve(s.ctx, "borrower", s.request.RequestId, time.Hour)
						results <- result{
							decision: d,
							err:      err,
						}
					},
				)
			}
			close(start)
			writers.Wait()
			close(results)
			successes, conflicts := 0, 0
			for value := range results {
				switch {
				case value.err == nil:
					successes++
					s.decision = value.decision
				case errors.Is(value.err, borrow.ErrConflict):
					conflicts++
				default:
					return value.err
				}
			}
			if successes != 1 || conflicts != 1 {
				return fmt.Errorf("concurrent grants: %d successes, %d conflicts", successes, conflicts)
			}
			_, afterHead, err := s.grants.decisions(s.ctx)
			if err != nil {
				return err
			}
			if afterHead != head.Next() {
				return errors.New("competing grant appended more than one outcome")
			}
			for _, service := range []*borrow.Service{s.service, s.pendingWriter} {
				d, err := service.Get(s.ctx, "borrower", s.request.RequestId)
				if err != nil {
					return err
				}
				if !proto.Equal(d, s.decision) {
					return errors.New("services disagree about the committed grant")
				}
			}
			return nil
		},
	)
	sc.Step(
		`^重启后仅恢复已提交的审批结果且 lease 可读取$`,
		func() error {
			if err := s.recoverGrant(); err != nil {
				return err
			}
			records, head, err := s.grants.decisions(s.ctx)
			if err != nil {
				return err
			}
			if len(records) != 2 || head != te.SeqFromUint64(2) {
				return errors.New("sidecar did not preserve exactly one pending admission and one approval")
			}
			_, err = s.client.ReadView(s.ctx, s.decision.Lease)
			return err
		},
	)
	sc.Step(
		`^未提交的候选 lease 不能读取或释放且 sidecar 不变$`,
		func() error {
			_, before, err := s.grants.decisions(s.ctx)
			if err != nil {
				return err
			}
			ref, err := (recoveredViews{
				Tape:  s.peerTape,
				Owner: "researcher-local",
				All:   true,
			}).View(s.ctx, "borrower", nil)
			if err != nil {
				return err
			}
			if ref == nil {
				return errors.New("no committed fixture view")
			}
			scopes := make([]tv.EntryRange, len(ref.Scopes))
			for i, scope := range ref.Scopes {
				scopes[i] = tv.EntryRange{
					SeqS: te.MustParseSeq(scope.SeqS),
					SeqE: te.MustParseSeq(scope.SeqE),
				}
			}
			views, err := s.grants.tape.OverView(owner.WithOwnerId(s.ctx, s.grants.owner), scopes...)
			if err != nil {
				return err
			}
			result, err := views.Handle(
				s.ctx,
				grant.Action,
				grant.Request{
					Caller: "borrower",
					TTL:    time.Hour,
				},
			)
			if err != nil {
				return err
			}
			lease, ok := result.(*pb.Lease)
			if !ok || lease == nil {
				return errors.New("native grant handler returned no candidate lease")
			}
			if _, err := s.client.ReadView(s.ctx, lease); !errors.Is(err, a2a.ErrUnavailable) {
				return fmt.Errorf("uncommitted lease read returned %v", err)
			}
			if err := s.client.Release(s.ctx, lease.Id); !errors.Is(err, a2a.ErrUnavailable) {
				return fmt.Errorf("uncommitted lease release returned %v", err)
			}
			_, after, err := s.grants.decisions(s.ctx)
			if err != nil {
				return err
			}
			if before != after {
				return errors.New("candidate issuance changed the borrowing journal")
			}
			return nil
		},
	)
	// Bounded cancellation is checked through the real handler and storage path.
	sc.Step(
		`^取消的授权请求不追加 sidecar 记录$`,
		func() error {
			_, before, err := s.grants.decisions(s.ctx)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(s.ctx, time.Nanosecond)
			cancel()
			_, err = s.service.Borrow(ctx, "borrower", &pb.BorrowRequest{
				RequestId: "cancelled-grant",
				Purpose:   "cancelled request",
				Query:     "committed context",
			})
			if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				return fmt.Errorf("cancelled grant returned %v", err)
			}
			_, after, err := s.grants.decisions(s.ctx)
			if err != nil {
				return err
			}
			if after != before {
				return errors.New("cancelled request appended a sidecar record")
			}
			return nil
		},
	)
}
