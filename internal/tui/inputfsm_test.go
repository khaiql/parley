package tui

import (
	"testing"
)

func TestInputFSM_StartsInNormal(t *testing.T) {
	fsm := NewInputFSM(nil, nil)
	if fsm.Current() != StateNormal {
		t.Errorf("expected StateNormal, got %v", fsm.Current())
	}
}

func TestInputFSM_SlashTransitionsToCompleting(t *testing.T) {
	fsm := NewInputFSM(func(_ InputTrigger) {}, func() {})
	if err := fsm.Fire(TriggerSlash); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fsm.Current() != StateCompleting {
		t.Errorf("expected StateCompleting, got %v", fsm.Current())
	}
}

func TestInputFSM_MentionTransitionsToCompleting(t *testing.T) {
	fsm := NewInputFSM(func(_ InputTrigger) {}, func() {})
	if err := fsm.Fire(TriggerMention); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fsm.Current() != StateCompleting {
		t.Errorf("expected StateCompleting, got %v", fsm.Current())
	}
}

func TestInputFSM_AcceptReturnsToNormal(t *testing.T) {
	fsm := NewInputFSM(func(_ InputTrigger) {}, func() {})
	_ = fsm.Fire(TriggerSlash)
	if err := fsm.Fire(TriggerAccept); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fsm.Current() != StateNormal {
		t.Errorf("expected StateNormal, got %v", fsm.Current())
	}
}

func TestInputFSM_DismissReturnsToNormal(t *testing.T) {
	fsm := NewInputFSM(func(_ InputTrigger) {}, func() {})
	_ = fsm.Fire(TriggerSlash)
	if err := fsm.Fire(TriggerDismiss); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fsm.Current() != StateNormal {
		t.Errorf("expected StateNormal, got %v", fsm.Current())
	}
}

func TestInputFSM_OnEnterCalledWithTrigger(t *testing.T) {
	var receivedTrigger InputTrigger
	called := false
	fsm := NewInputFSM(func(trigger InputTrigger) {
		called = true
		receivedTrigger = trigger
	}, func() {})

	_ = fsm.Fire(TriggerSlash)
	if !called {
		t.Fatal("onEnterCompleting was not called")
	}
	if receivedTrigger != TriggerSlash {
		t.Errorf("expected TriggerSlash, got %v", receivedTrigger)
	}

	// Reset and test with mention
	called = false
	_ = fsm.Fire(TriggerDismiss)
	_ = fsm.Fire(TriggerMention)
	if !called {
		t.Fatal("onEnterCompleting was not called for mention")
	}
	if receivedTrigger != TriggerMention {
		t.Errorf("expected TriggerMention, got %v", receivedTrigger)
	}
}

func TestInputFSM_OnExitCalledOnDismiss(t *testing.T) {
	exitCalled := false
	fsm := NewInputFSM(func(_ InputTrigger) {}, func() {
		exitCalled = true
	})

	_ = fsm.Fire(TriggerSlash)
	_ = fsm.Fire(TriggerDismiss)
	if !exitCalled {
		t.Fatal("onExitCompleting was not called")
	}
}

func TestInputFSM_InvalidTransitionFromNormal(t *testing.T) {
	fsm := NewInputFSM(func(_ InputTrigger) {}, func() {})
	err := fsm.Fire(TriggerAccept)
	if err == nil {
		t.Fatal("expected error for invalid transition, got nil")
	}
}
