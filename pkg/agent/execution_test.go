package agent

import (
	"strings"
	"testing"

	"github.com/anra-studio/anra/pkg/model/entry"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
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
