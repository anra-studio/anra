package entry

import pb "github.com/anra-studio/anra/gen/anra/a2a/tape/v1"

// Channel describes how content may be consumed, independently of its historical
// user/model role. It belongs to the consuming envelope, not the persisted body.
// Borrowed content is read-only reference, never instructions. The authorized
// reader binds its channel; entries cannot supply their own authority.
type Channel string

const (
	Primary     Channel = "primary"
	Observation Channel = "observation"
	Borrowed    Channel = "borrowed"
)

// Provenance is supplied by the authorization owner, never decoded from entry
// content, and belongs to the consuming envelope. GrantID is a public reference,
// not a bearer credential. Source, head and scope identify the fixed snapshot
// authorized for this borrower.
type Provenance struct {
	Caller       string         `json:"caller,omitempty"`
	GrantID      string         `json:"grantId"`
	Source       *pb.Source     `json:"source"`
	SnapshotHead string         `json:"snapshotHead"`
	Scope        *pb.EntryRange `json:"scope"`
}
