package e2e_test

import (
	"fmt"
	"github.com/anra-studio/anra/pkg/model/anchor"
	"github.com/scbizu/tape-go/pkg/tape/entry"
)

// inspectLifecycle verifies the physical records before projecting conversation.
func inspectLifecycle(entries []entry.EntryLike) ([]entry.EntryLike, error) {
	var conversation []entry.EntryLike
	var active *anchor.Record
	initialized, starts, closes := 0, 0, 0
	var startSeq string
	calls, results := 0, 0
	for _, e := range entries {
		r, err := anchor.Decode(e)
		if err != nil {
			return nil, err
		}
		if r != nil {
			switch r.Type {
			case anchor.TapeInitialized:
				initialized++
			case anchor.TurnStarted:
				if active != nil {
					return nil, fmt.Errorf("overlapping starts at %s", e.GetID())
				}
				active = r
				startSeq = e.GetID().String()
				starts++
			case anchor.TurnClosed:
				if active == nil || active.TurnID != r.TurnID || r.StartSeq != startSeq {
					return nil, fmt.Errorf("unmatched close at %s", e.GetID())
				}
				active = nil
				closes++
			}
			continue
		}
		if active == nil {
			return nil, fmt.Errorf("entry %s outside turn", e.GetID())
		}
		switch e.GetKind() {
		case entry.EntryUser, entry.EntryAssistant:
			conversation = append(conversation, e)
		case entry.EntryToolCall:
			calls++
		case entry.EntryToolResult:
			results++
		default:
			return nil, fmt.Errorf("unexpected entry kind %s", e.GetKind())
		}
	}
	if initialized != 1 || active != nil || starts != closes || calls != results {
		return nil, fmt.Errorf("invalid lifecycle: init=%d starts=%d closes=%d calls=%d results=%d", initialized, starts, closes, calls, results)
	}
	return conversation, nil
}
