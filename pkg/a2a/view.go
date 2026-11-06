package a2a

import (
	sdk "github.com/a2aproject/a2a-go/v2/a2a"
	pb "github.com/anra-studio/anra/gen/anra/model/view/v1"
	"github.com/anra-studio/anra/pkg/model/view"
	"google.golang.org/protobuf/proto"
)

const (
	ContextMediaType = view.ContextMediaType
	AnchorMediaType  = view.AnchorMediaType
)

func AdvertiseViews(card *sdk.AgentCard, endpoint string) error {
	if err := Advertise(card, endpoint); err != nil {
		return err
	}
	for i := range card.Capabilities.Extensions {
		ext := &card.Capabilities.Extensions[i]
		if ext.URI == ExtensionURI {
			ext.Params["viewBorrowVersion"] = 1
			ext.Params["entryMediaTypes"] = []string{TextMediaType, ContextMediaType, AnchorMediaType}
		}
	}
	return nil
}

func viewMatches(ref *pb.Ref, lease *pb.Lease) bool {
	return lease != nil && proto.Equal(ref, lease.Ref)
}
