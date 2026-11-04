package mcp

import (
	"strings"
	"testing"

	"github.com/go-playground/validator/v10"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
)

func TestAnchorSequenceValidationMatchesTape(t *testing.T) {
	standalone := validator.New(validator.WithRequiredStructEnabled())
	for _, value := range []string{
		"", "0", "01", "-1", "+1", "1.0", "1e3", "1\n", "１",
		"1", "18446744073709551616", strings.Repeat("9", 100),
	} {
		seq, err := te.ParseSeq(value)
		want := err == nil && !seq.IsZero()
		if got := validate.Struct(toolInput{AnchorSeq: value}) == nil; got != want {
			t.Errorf("tool input %q valid = %t; Tape nonzero sequence = %t", value, got, want)
		}
		if got := standalone.StructPartial(Grant{AnchorSeq: value}, "AnchorSeq") == nil; got != want {
			t.Errorf("grant %q valid = %t; Tape nonzero sequence = %t", value, got, want)
		}
	}
}
