package a2a

import (
	"context"
	"math"
	"time"

	te "github.com/scbizu/tape-go/pkg/tape/entry"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
)

// Score addresses a candidate by index. Adapter implementations may call JEV
// or any decision API; the provider receives only already permitted summaries.
type Score struct {
	Index      int
	Score      float64
	Confidence float64
}
type Ranker interface {
	Rank(context.Context, string, []string) ([]Score, error)
}
type RankerFunc func(context.Context, string, []string) ([]Score, error)

func (f RankerFunc) Rank(c context.Context, q string, v []string) ([]Score, error) { return f(c, q, v) }
func best(ctx context.Context, r Ranker, q string, candidates []string) (int, error) {
	if len(candidates) == 0 {
		return -1, nil
	}
	if r == nil {
		return -1, ErrInvalidParams
	}
	scores, err := r.Rank(ctx, q, candidates)
	if err != nil {
		return -1, err
	}
	selected := -1
	var value, confidence float64
	seen := map[int]bool{}
	for _, s := range scores {
		if s.Index < 0 || s.Index >= len(candidates) || seen[s.Index] || math.IsNaN(s.Score) || math.IsInf(s.Score, 0) || math.IsNaN(s.Confidence) || math.IsInf(s.Confidence, 0) {
			return -1, ErrInvalidParams
		}
		seen[s.Index] = true
		if selected < 0 || s.Score > value || (s.Score == value && (s.Confidence > confidence || (s.Confidence == confidence && s.Index < selected))) {
			selected = s.Index
			value = s.Score
			confidence = s.Confidence
		}
	}
	return selected, nil
}

type Candidate struct {
	Summary string
	Range   tv.EntryRange
}

// Eligible must check sharing permission and exportability before returning
// candidate summaries. Ranking never grants permission by itself.
type Eligible func(context.Context, string, BorrowRequest, te.Seq) ([]Candidate, error)
type BestRangePolicy struct {
	Eligible Eligible
	Ranker   Ranker
	TTL      time.Duration
}

func (p BestRangePolicy) Select(ctx context.Context, requester string, req BorrowRequest, head te.Seq) (Selection, error) {
	if p.Eligible == nil {
		return Selection{}, ErrInvalidParams
	}
	cs, err := p.Eligible(ctx, requester, req, head)
	if err != nil {
		return Selection{}, err
	}
	permitted := make([]Candidate, 0, len(cs))
	summaries := make([]string, 0, len(cs))
	for _, c := range cs {
		if !validRange(c.Range, head) {
			return Selection{}, ErrInvalidParams
		}
		if req.Bounds != nil && (c.Range.SeqS.Cmp(req.Bounds.SeqS) < 0 || c.Range.SeqE.Cmp(req.Bounds.SeqE) > 0) {
			continue
		}
		permitted = append(permitted, c)
		summaries = append(summaries, c.Summary)
	}
	index, err := best(ctx, p.Ranker, req.Query, summaries)
	if err != nil {
		return Selection{}, err
	}
	if index < 0 {
		return Selection{Status: Denied}, nil
	}
	return Selection{Status: Granted, Range: &permitted[index].Range, TTL: p.TTL}, nil
}
