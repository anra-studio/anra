package view

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/anra-studio/anra/pkg/model/entry"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
)

func TestContextExporterPreservesTextAndEmptyParts(t *testing.T) {
	t.Parallel()

	emptyModel := entry.FromEntry(te.NewEntry(te.WithEntryKind(te.EntryAssistant))).
		WithExtension(entry.ADKEvent, `{"content":{"role":"model","parts":[]}}`)
	for _, tc := range []struct {
		name string
		raw  entry.Entry
		role string
		text string
	}{
		{
			name: "plain user text",
			raw: entry.FromEntry(te.NewEntry(
				te.WithEntryKind(te.EntryUser),
				te.WithEntryContent("Original user content"),
			)),
			role: "user",
			text: "Original user content",
		},
		{
			name: "plain model text",
			raw: entry.FromEntry(te.NewEntry(
				te.WithEntryKind(te.EntryAssistant),
				te.WithEntryContent("Original model content"),
			)),
			role: "model",
			text: "Original model content",
		},
		{
			name: "model event with empty parts",
			raw:  emptyModel,
			role: "model",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seq := te.SeqFromUint64(7)
			stamp := time.Unix(100, 0).UTC()
			raw, err := entry.FromTape(tc.raw.WithID(seq).WithTimestamp(stamp))
			if err != nil {
				t.Fatal(err)
			}
			exported, err := (ContextExporter{}).Export(raw)
			if err != nil {
				t.Fatal(err)
			}
			if exported.Seq != seq.String() || exported.Kind != string(raw.GetKind()) || !exported.Timestamp.Equal(stamp) || exported.MediaType != ContextMediaType {
				t.Fatalf("export changed entry metadata: %+v", exported)
			}
			var content entry.Content
			if err := json.Unmarshal(exported.Content, &content); err != nil {
				t.Fatal(err)
			}
			if content.Role != tc.role {
				t.Errorf("content role = %q, want %q", content.Role, tc.role)
			}
			if tc.text == "" {
				if len(content.Parts) != 0 {
					t.Errorf("empty parts became %+v", content.Parts)
				}
			} else if len(content.Parts) != 1 || content.Parts[0].Text != tc.text || content.Parts[0].Thought {
				t.Errorf("original text changed: %s", exported.Content)
			}
			encoded, err := json.Marshal(exported)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(encoded, []byte(`"summary":`)) {
				t.Errorf("export duplicated the original text: %s", encoded)
			}
		})
	}
}
