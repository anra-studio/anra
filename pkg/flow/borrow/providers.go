package borrow

import (
	"context"

	pb "github.com/anra-studio/anra/gen/anra/model/view/v1"
	"github.com/anra-studio/anra/pkg/model/entry"
	"github.com/anra-studio/anra/pkg/model/view"
)

// Views resolves an exact committed Ref, or nil when no view is eligible.
// Providers own commitment and canonical scope ordering; results stay immutable.
type Views interface {
	View(context.Context, string, *pb.Ref) (*pb.Ref, error)
}

// Exporter converts one stored entry into public view content.
type Exporter interface {
	Export(entry.Entry) (view.Entry, error)
}
