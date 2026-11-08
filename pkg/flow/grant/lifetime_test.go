package grant

import (
	"testing"
	"time"

	pb "github.com/anra-studio/anra/gen/anra/model/view/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestLeaseExpiresAtDeadline(t *testing.T) {
	t.Parallel()
	expires := time.Unix(100, 0)
	state := State{
		Lease: &pb.Lease{
			ExpiresAt: timestamppb.New(expires),
		},
	}
	if !state.Active(expires.Add(-time.Nanosecond)) {
		t.Fatal("lease expired before its deadline")
	}
	if state.Active(expires) {
		t.Fatal("lease remained active at its deadline")
	}
	state.Released = true
	if state.Active(expires.Add(-time.Nanosecond)) {
		t.Fatal("release did not terminate the lease")
	}
}
