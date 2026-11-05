package projection

import pb "github.com/anra-studio/anra/gen/anra/a2a/tape/v1"

// Channel describes how content may be consumed, independently of its historical
// user/model role. Borrowed content is read-only reference, never instructions.
type Channel string

const (
	Primary     Channel = "primary"
	Observation Channel = "observation"
	Borrowed    Channel = "borrowed"
)

// Provenance is supplied by the authorization owner, never decoded from entry
// content. GrantID is a public reference, not a bearer credential. Source, head
// and scope identify the fixed snapshot authorized for this borrower.
type Provenance struct {
	Caller       string         `json:"caller,omitempty"`
	GrantID      string         `json:"grantId"`
	Source       *pb.Source     `json:"source"`
	SnapshotHead string         `json:"snapshotHead"`
	Scope        *pb.EntryRange `json:"scope"`
}
