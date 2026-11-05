package anchor

import (
	"github.com/anra-studio/anra/pkg/model/entry"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
)

// Export returns the public content of a custom anchor selected by its caller.
// Native execution records omit the private ownership epoch. Other schemas
// retain their original body as text; no schema-independent redaction is done.
// The original stored record remains unchanged.
func Export(e te.EntryLike) (any, error) {
	record, err := Decode(e)
	if err != nil {
		return nil, err
	}
	if record == nil {
		return entry.FromTape(e).Body(), nil
	}
	return record.Body(), nil
}
