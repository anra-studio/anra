package anchor

import (
	"fmt"

	"github.com/anra-studio/anra/pkg/model/entry"
)

// Export returns the public native record. Unsupported schemas and non-anchor
// entries return ErrUnsupported.
func Export(e entry.Entry) (*BuiltIn, error) {
	if e.GetKind() != Kind {
		return nil, fmt.Errorf("%w: entry kind %q", ErrUnsupported, e.GetKind())
	}
	record, err := Decode(e)
	if err != nil {
		return nil, err
	}
	return record.Export(), nil
}
