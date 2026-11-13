package borrow

import "github.com/looplab/fsm"

const newState = "new"

// requestStateMachine owns request admission and pending completion. Lease state
// and read authorization belong to grant.
func requestStateMachine(state string) *fsm.FSM {
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
