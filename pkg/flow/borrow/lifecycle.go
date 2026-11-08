package borrow

import "github.com/looplab/fsm"

const newState = "new"

// lifecycle owns request admission and pending completion. Lease lifetime and
// read authorization belong to grant, not to this request state machine.
func lifecycle(state string) *fsm.FSM {
	return fsm.NewFSM(
		state,
		fsm.Events{
			{
				Name: Pending.String(),
				Src:  []string{newState},
				Dst:  Pending.String(),
			},
			{
				Name: Granted.String(),
				Src:  []string{newState, Pending.String()},
				Dst:  Granted.String(),
			},
			{
				Name: Denied.String(),
				Src:  []string{newState, Pending.String()},
				Dst:  Denied.String(),
			},
		},
		nil,
	)
}
