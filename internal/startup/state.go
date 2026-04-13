package startup

import "sync/atomic"

type State struct {
	ready atomic.Bool
}

func NewState() *State {
	return &State{}
}

func (state *State) MarkReady() {
	if state == nil {
		return
	}

	state.ready.Store(true)
}

func (state *State) Ready() bool {
	if state == nil {
		return true
	}

	return state.ready.Load()
}
