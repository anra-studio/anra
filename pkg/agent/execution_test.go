package agent

import (
	"strings"
	"testing"

	"github.com/anra-studio/anra/pkg/model/entry"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
)

func TestExecutionRejectsConflictingRecords(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{
			name: "unmatched close",
			body: `{"schema":"anra.dev/anchor/v1","type":"turn.closed","turnId":"unknown","startSeq":"1","outcome":"completed"}`,
			want: "unmatched turn record",
		},
		{
			name: "duplicate source",
			body: `{"schema":"anra.dev/anchor/v1","type":"tape.initialized","source":{"agentId":"alice","tapeId":"replacement"}}`,
			want: "conflicting source identity",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &Anra{
				role: "alice",
			}
			initial := entry.FromEntry(te.NewAnchor(
				te.SeqFromUint64(1),
				"alice",
				te.AnchorKindCustom,
				[]byte(`{"schema":"anra.dev/anchor/v1","type":"tape.initialized","source":{"agentId":"alice","tapeId":"original"}}`),
			))
			if err := a.apply(initial); err != nil {
				t.Fatal(err)
			}
			record := entry.FromEntry(te.NewAnchor(
				te.SeqFromUint64(3),
				"alice",
				te.AnchorKindCustom,
				[]byte(tc.body),
			))
			if err := a.apply(record); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("invalid transition returned %v, want %q", err, tc.want)
			}
			if a.execution.source.GetTapeId() != "original" || a.execution.active != nil || a.execution.latest != nil {
				t.Fatal("rejected transition changed execution state")
			}
		})
	}
}

func TestExternalAnchorDoesNotDriveNativeExecution(t *testing.T) {
	t.Parallel()

	a := &Anra{
		role: "alice",
		execution: execution{
			seen:  make(map[string]bool),
			views: make(map[string]tv.TapeView),
		},
	}
	records := []string{
		`{"schema":"anra.dev/anchor/v1","type":"tape.initialized","source":{"agentId":"alice","tapeId":"original"}}`,
		`{"schema":"anra.dev/anchor/v1","type":"turn.started","turnId":"one","ownerEpoch":"owner"}`,
		`{"schema":"example.org/anchor/v1","type":"turn.closed","turnId":42,"source":"integration-owned"}`,
		`{"schema":"anra.dev/anchor/v1","type":"turn.closed","turnId":"one","startSeq":"2","outcome":"completed"}`,
	}
	for i, raw := range records {
		value := entry.FromEntry(te.NewAnchor(
			te.SeqFromUint64(uint64(i+1)),
			"alice",
			te.AnchorKindCustom,
			[]byte(raw),
		))
		if err := a.apply(value); err != nil {
			t.Fatalf("replay at %d: %v", i+1, err)
		}
	}
	ref := a.execution.latest
	if a.execution.active != nil || ref.GetSource().GetTapeId() != "original" || len(ref.GetScopes()) != 1 ||
		ref.Scopes[0].SeqS != "3" || ref.Scopes[0].SeqE != "4" {
		t.Fatalf("external anchor changed native execution: active=%v, ref=%v", a.execution.active, ref)
	}
}
