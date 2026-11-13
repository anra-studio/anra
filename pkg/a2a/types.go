// Package a2a adapts Anra peers and shared committed views to A2A and Connect.
package a2a

import (
	pb "github.com/anra-studio/anra/gen/anra/model/view/v1"
	"github.com/anra-studio/anra/pkg/model/view"
)

const (
	ExtensionURI    = "urn:anra:a2a:tape-view:v1"
	ExtensionHeader = "A2A-Extensions"
	TextMediaType   = view.TextMediaType
)

const (
	Granted = pb.DecisionStatus_DECISION_STATUS_GRANTED
	Denied  = pb.DecisionStatus_DECISION_STATUS_DENIED
	Pending = pb.DecisionStatus_DECISION_STATUS_PENDING
)
