package herdr

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type NotificationCoverage struct {
	Key        string `json:"key"`
	SessionID  string `json:"session_id"`
	SeenSeq    int64  `json:"seen_seq"`
	ThroughSeq int64  `json:"through_seq"`
}

type NotificationAttempt struct {
	ID             string                 `json:"id"`
	MachineID      string                 `json:"machine_id"`
	HerdrSocket    string                 `json:"herdr_socket"`
	Conversation   NativeConversation     `json:"conversation"`
	Coverage       []NotificationCoverage `json:"coverage"`
	Outcome        string                 `json:"outcome"`
	Message        string                 `json:"message,omitempty"`
	Replaces       string                 `json:"replaces,omitempty"`
	RetryRequested bool                   `json:"retry_requested,omitempty"`
}

func (a NotificationAttempt) validate() error {
	if safeID(a.ID) != nil || safeID(a.MachineID) != nil || a.Conversation.validate() != nil || len(a.Coverage) == 0 || len(a.Coverage) > 16 || len(a.Message) > 4096 {
		return fmt.Errorf("invalid notification attempt")
	}
	if _, err := SessionKey(a.HerdrSocket); err != nil {
		return err
	}
	switch a.Outcome {
	case "prepared", "accepted", "uncertain", "rejected":
	default:
		return fmt.Errorf("invalid notification outcome")
	}
	if a.Replaces != "" && (a.Replaces == a.ID || safeID(a.Replaces) != nil) {
		return fmt.Errorf("invalid predecessor")
	}
	keys := map[string]bool{}
	for _, c := range a.Coverage {
		if !validDigest(c.Key) || safeID(c.SessionID) != nil || c.SeenSeq < 0 || c.ThroughSeq <= c.SeenSeq || keys[c.Key] {
			return fmt.Errorf("invalid notification coverage")
		}
		keys[c.Key] = true
	}
	return nil
}

func (s *Store) NotificationAttempts() ([]NotificationAttempt, error) {
	dir, err := s.notificationDir("attempts")
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := []NotificationAttempt{}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		var a NotificationAttempt
		if err = readJSON(filepath.Join(dir, entry.Name()), &a); err != nil {
			return nil, err
		}
		if err = a.validate(); err != nil {
			return nil, err
		}
		if entry.Name() != a.ID+".json" {
			return nil, fmt.Errorf("notification attempt filename mismatch")
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func notificationReplaced(attempts []NotificationAttempt) map[string]bool {
	result := map[string]bool{}
	for _, a := range attempts {
		if a.Replaces != "" {
			result[a.Replaces] = true
		}
	}
	return result
}

func notificationOutstanding(key string, seen int64, attempts []NotificationAttempt) (*NotificationAttempt, error) {
	replaced := notificationReplaced(attempts)
	for _, a := range attempts {
		if replaced[a.ID] || a.Outcome == "rejected" {
			continue
		}
		for _, c := range a.Coverage {
			if c.Key == key {
				if seen < c.SeenSeq {
					return nil, fmt.Errorf("notification_cursor_regression")
				}
				if seen < c.ThroughSeq {
					outstanding := a
					return &outstanding, nil
				}
			}
		}
	}
	return nil, nil
}

func (s *Store) PrepareNotification(ctx context.Context, machine, socket string, observations []NotificationObservation, replaces string) (NotificationAttempt, error) {
	var result NotificationAttempt
	if len(observations) == 0 {
		return result, fmt.Errorf("notification coverage required")
	}
	native := observations[0].Conversation
	lock := hashBytes([]byte(machine + "\x00" + socket + "\x00" + native.Source + "\x00" + native.Agent + "\x00" + native.Kind + "\x00" + native.Value))
	err := s.WithBinding(ctx, "notification:"+lock, func() error {
		attempts, err := s.NotificationAttempts()
		if err != nil {
			return err
		}
		if replaces != "" {
			found := false
			for _, a := range attempts {
				if a.ID == replaces && a.RetryRequested && a.MachineID == machine && a.HerdrSocket == socket && a.Conversation == native && !notificationReplaced(attempts)[a.ID] {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("notification_retry_unavailable")
			}
		}
		id, err := NewID()
		if err != nil {
			return err
		}
		result = NotificationAttempt{ID: id, MachineID: machine, HerdrSocket: socket, Conversation: native, Outcome: "prepared", Replaces: replaces}
		for _, o := range observations {
			if o.Conversation != native {
				return fmt.Errorf("notification_recipient_mismatch")
			}
			prior, err := notificationOutstanding(o.Key, o.SeenSeq, attempts)
			if err != nil {
				return err
			}
			if prior != nil && prior.ID != replaces {
				return fmt.Errorf("notification_outstanding")
			}
			if replaces != "" {
				allowed := false
				for _, a := range attempts {
					if a.ID == replaces {
						for _, coverage := range a.Coverage {
							if coverage.Key == o.Key && coverage.SessionID == o.Binding.SessionID {
								allowed = true
							}
						}
					}
				}
				if !allowed {
					return fmt.Errorf("notification_retry_binding_mismatch")
				}
			}
			result.Coverage = append(result.Coverage, NotificationCoverage{Key: o.Key, SessionID: o.Binding.SessionID, SeenSeq: o.SeenSeq, ThroughSeq: o.ThroughSeq})
		}
		if err = result.validate(); err != nil {
			return err
		}
		dir, err := s.notificationDir("attempts")
		if err != nil {
			return err
		}
		return writeJSON(filepath.Join(dir, id+".json"), result)
	})
	return result, err
}

func (s *Store) CompleteNotification(id, outcome, message string) error {
	return s.updateNotification(context.Background(), id, func(a *NotificationAttempt) error {
		if a.Outcome != "prepared" {
			return fmt.Errorf("notification_already_completed")
		}
		a.Outcome = outcome
		a.Message = message
		return a.validate()
	})
}

func (s *Store) updateNotification(ctx context.Context, id string, update func(*NotificationAttempt) error) error {
	if safeID(id) != nil {
		return fmt.Errorf("invalid attempt id")
	}
	dir, err := s.notificationDir("attempts")
	if err != nil {
		return err
	}
	return lockFile(ctx, filepath.Join(dir, id+".lock"), func() error {
		var a NotificationAttempt
		path := filepath.Join(dir, id+".json")
		if err := readJSON(path, &a); err != nil {
			return err
		}
		if err := a.validate(); err != nil {
			return err
		}
		if a.ID != id {
			return fmt.Errorf("notification attempt mismatch")
		}
		if err := update(&a); err != nil {
			return err
		}
		return writeJSON(path, a)
	})
}

func (s *Store) RetryNotification(ctx context.Context, id string) error {
	return s.updateNotification(ctx, id, func(a *NotificationAttempt) error {
		if a.Outcome != "prepared" && a.Outcome != "uncertain" {
			return fmt.Errorf("notification_retry_unavailable")
		}
		attempts, err := s.NotificationAttempts()
		if err != nil {
			return err
		}
		if a.RetryRequested || notificationReplaced(attempts)[id] {
			return fmt.Errorf("notification_retry_unavailable")
		}
		a.RetryRequested = true
		return nil
	})
}
