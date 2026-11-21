package anchor

import (
	"errors"
	"strings"
	"testing"

	"github.com/anra-studio/anra/pkg/model/entry"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
)

func TestUnknownSchema(t *testing.T) {
	t.Parallel()

	for _, schema := range []string{"anra.dev/anchor/intr/v1", "anra.dev/anchor/handoff/v1", "anra.dev/anchor/v2", "example.org/anchor/v1"} {
		t.Run(schema, func(t *testing.T) {
			raw := `{"schema":"` + schema + `","type":"turn.started"}`
			e := testAnchor(raw)
			if value, err := Decode(e); !errors.Is(err, ErrUnsupported) || value != nil {
				t.Fatalf("Decode: got %v, %v; want nil, ErrUnsupported", value, err)
			}
			if value, err := Export(e); !errors.Is(err, ErrUnsupported) || value != nil {
				t.Fatalf("Export: got %v, %v; want nil, ErrUnsupported", value, err)
			}
		})
	}
}

func TestDecodeRejectsCorruptAnchors(t *testing.T) {
	t.Parallel()

	for name, payload := range map[string]string{
		"missing schema":        `{"type":"turn.started"}`,
		"truncated envelope":    `{"schema":"example.org/anchor/v1","type":`,
		"wrong type":            `{"schema":"anra.dev/anchor/v1","type":1}`,
		"unknown type":          `{"schema":"anra.dev/anchor/v1","type":"integration.created"}`,
		"missing turn identity": `{"schema":"anra.dev/anchor/v1","type":"turn.started"}`,
		"missing source":        `{"schema":"anra.dev/anchor/v1","type":"tape.initialized"}`,
		"invalid outcome":       `{"schema":"anra.dev/anchor/v1","type":"turn.closed","turnId":"one","startSeq":"1","outcome":"unknown"}`,
		"forbidden field":       `{"schema":"anra.dev/anchor/v1","type":"turn.started","turnId":"one","ownerEpoch":"old","outcome":"failed"}`,
		"oversized identity":    `{"schema":"anra.dev/anchor/v1","type":"turn.started","ownerEpoch":"old","turnId":"` + strings.Repeat("界", 86) + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if value, err := Decode(testAnchor(payload)); value != nil || err == nil || errors.Is(err, ErrUnsupported) {
				t.Fatalf("corrupt anchor decoded as %v, %v", value, err)
			}
		})
	}
}

func testAnchor(body string) entry.Entry {
	return entry.FromEntry(te.NewAnchor(
		te.SeqFromUint64(3),
		"owner",
		te.AnchorKindCustom,
		[]byte(body),
	))
}
