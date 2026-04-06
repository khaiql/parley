package tui

import (
	"context"

	"github.com/qmuntal/stateless"
)

// InputState represents the current state of the input field.
type InputState int

const (
	StateNormal     InputState = iota
	StateCompleting            // Autocomplete menu is visible
)

// InputTrigger represents events that cause state transitions.
type InputTrigger int

const (
	TriggerSlash   InputTrigger = iota // '/' typed at position 0
	TriggerMention                     // '@' typed after whitespace
	TriggerAccept                      // Tab pressed
	TriggerDismiss                     // Esc pressed
	TriggerSubmit                      // Enter pressed
)

// InputFSM manages input interaction modes using a formal state machine.
type InputFSM struct {
	machine *stateless.StateMachine
}

// NewInputFSM creates an InputFSM with injected callbacks for state transitions.
// onEnterCompleting is called when entering the completing state, with the trigger that caused it.
// onExitCompleting is called when leaving the completing state.
func NewInputFSM(
	onEnterCompleting func(trigger InputTrigger),
	onExitCompleting func(),
) *InputFSM {
	sm := stateless.NewStateMachine(StateNormal)

	sm.Configure(StateNormal).
		Permit(TriggerSlash, StateCompleting).
		Permit(TriggerMention, StateCompleting)

	completing := sm.Configure(StateCompleting).
		Permit(TriggerAccept, StateNormal).
		Permit(TriggerDismiss, StateNormal).
		Permit(TriggerSubmit, StateNormal)

	if onEnterCompleting != nil {
		completing.OnEntry(func(_ context.Context, args ...any) error {
			if len(args) > 0 {
				if trigger, ok := args[0].(InputTrigger); ok {
					onEnterCompleting(trigger)
				}
			}
			return nil
		})
	}

	if onExitCompleting != nil {
		completing.OnExit(func(_ context.Context, _ ...any) error {
			onExitCompleting()
			return nil
		})
	}

	return &InputFSM{machine: sm}
}

// Current returns the current input state.
func (f *InputFSM) Current() InputState {
	return f.machine.MustState().(InputState)
}

// Fire triggers a state transition. Returns an error if the transition is invalid.
func (f *InputFSM) Fire(trigger InputTrigger) error {
	return f.machine.Fire(trigger, trigger)
}
