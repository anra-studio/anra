package a2a

import (
	"context"
	"sort"
)

// Ranker owns relevance ordering and the cut-off. It returns the indexes of the
// candidates worth considering for the query, best first, each at most once.
// Candidates it omits are not relevant enough (a top-N limit or a threshold is
// the Ranker's own policy); an empty result means no match. Scores, if the
// implementation has any, are an internal detail and never cross this interface.
//
// A Ranker is trusted to honor that contract; callers do not re-validate it.
// Vendor responses are parsed by the client that owns the vendor protocol, which
// returns well-formed indexes. Indexes
// can only select among candidates the caller already permitted, so a faulty
// Ranker can pick a worse permitted candidate but never an unpermitted one.
type Ranker interface {
	Rank(ctx context.Context, query string, candidates []string) ([]int, error)
}
type RankerFunc func(context.Context, string, []string) ([]int, error)

func (f RankerFunc) Rank(c context.Context, q string, v []string) ([]int, error) { return f(c, q, v) }

// rank ranks candidates, short-circuiting when there is nothing to rank.
func rank(ctx context.Context, r Ranker, q string, candidates []string) ([]int, error) {
	if len(candidates) == 0 {
		return nil, nil
	}
	if r == nil {
		return nil, ErrInvalidParams
	}
	return r.Rank(ctx, q, candidates)
}

// Score is the intermediate result of a scoring decision API, addressing a
// candidate by index.
type Score struct {
	Index      int
	Score      float64
	Confidence float64
}

// Scorer is implemented by decision APIs that score candidates, such as the
// decision classifier. Use ScoreRanker to turn one into a Ranker. Like Ranker,
// a Scorer is trusted: each Index addresses a candidate, at most once, and
// scores are finite. Parsing a vendor response into that shape is the job of the
// client that owns the vendor protocol (for example the JEV client, which derives
// indexes from its own request), not of the layers above it.
type Scorer interface {
	Score(ctx context.Context, query string, candidates []string) ([]Score, error)
}
type ScorerFunc func(context.Context, string, []string) ([]Score, error)

func (f ScorerFunc) Score(c context.Context, q string, v []string) ([]Score, error) {
	return f(c, q, v)
}

// ScoreRanker ranks by a Scorer's output: highest score first, then highest
// confidence, then original candidate order. Scores are comparable only within
// one call, so MinScore is meaningful only for scorers with calibrated scores.
type ScoreRanker struct {
	Scorer Scorer
	// Limit keeps at most this many best candidates; zero or less keeps all.
	Limit int
	// MinScore drops candidates scoring below it; nil applies no threshold.
	MinScore *float64
}

func (r ScoreRanker) Rank(ctx context.Context, q string, candidates []string) ([]int, error) {
	if r.Scorer == nil {
		return nil, ErrInvalidParams
	}
	scores, err := r.Scorer.Score(ctx, q, candidates)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(scores, func(i, j int) bool {
		a, b := scores[i], scores[j]
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if a.Confidence != b.Confidence {
			return a.Confidence > b.Confidence
		}
		return a.Index < b.Index
	})
	order := make([]int, 0, len(scores))
	for _, s := range scores {
		if r.MinScore != nil && s.Score < *r.MinScore {
			break
		}
		if r.Limit > 0 && len(order) == r.Limit {
			break
		}
		order = append(order, s.Index)
	}
	return order, nil
}
