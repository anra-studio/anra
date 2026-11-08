package anchor

import (
	"errors"
	"testing"

	"github.com/anra-studio/anra/pkg/model/entry"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
)

func TestUnknownSchema(t *testing.T) {
	e := entry.FromEntry(te.NewAnchor(te.Seq{}, "owner", te.AnchorKindCustom, []byte(`{"schema":"example.org/anchor/v1"}`)))
	if _, err := Decode(e); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Decode: got %v, want ErrUnsupported", err)
	}
	value, err := Export(e)
	if !errors.Is(err, ErrUnsupported) || value != nil {
		t.Fatalf("Export: got %v, %v; want nil, ErrUnsupported", value, err)
	}
}
