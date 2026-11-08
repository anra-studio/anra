package borrow

import (
	"context"
	"testing"
)

func TestRequestApprovalIsTerminal(t *testing.T) {
	t.Parallel()
	machine := lifecycle(newState)
	if err := machine.Event(context.Background(), Pending.String()); err != nil {
		t.Fatal(err)
	}
	if machine.Can(Pending.String()) {
		t.Fatal("a pending request cannot be deferred again")
	}
	if err := machine.Event(context.Background(), Granted.String()); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{Pending.String(), Granted.String(), Denied.String()} {
		if machine.Can(status) {
			t.Fatalf("completed request permits another outcome: %s", status)
		}
	}
}
