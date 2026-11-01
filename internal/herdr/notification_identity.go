package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/khaiql/parley/internal/model"
	parleyRuntime "github.com/khaiql/parley/internal/runtime"
)

// NativeConversation is independent of the terminal currently hosting it.
type NativeConversation struct {
	Source string `json:"source"`
	Agent  string `json:"agent"`
	Kind   string `json:"kind"`
	Value  string `json:"value"`
}

func (n NativeConversation) validate() error {
	if n.Source == "" || len(n.Source) > 80 || safeID(n.Agent) != nil || n.Agent == "shell" || (n.Kind != "id" && n.Kind != "path") || n.Value == "" || len(n.Value) > 4096 || !utf8.ValidString(n.Value) || strings.ContainsAny(n.Source+n.Value, "\x00\r\n") {
		return fmt.Errorf("native_identity_required")
	}
	return nil
}

type notificationAgent struct {
	PaneID         string              `json:"pane_id"`
	WorkspaceID    string              `json:"workspace_id"`
	TerminalID     string              `json:"terminal_id"`
	Kind           string              `json:"agent"`
	Status         string              `json:"agent_status"`
	StateChangeSeq uint64              `json:"state_change_seq"`
	Conversation   *NativeConversation `json:"agent_session,omitempty"`
}

type NotificationBinding struct {
	Key          string             `json:"key"`
	Binding      Binding            `json:"binding"`
	Conversation NativeConversation `json:"conversation"`
}

type NotificationObservation struct {
	NotificationBinding
	Agent      notificationAgent `json:"agent"`
	SeenSeq    int64             `json:"seen_seq"`
	ThroughSeq int64             `json:"through_seq"`
}

func (h *HostOperations) notificationAgents(ctx context.Context) ([]notificationAgent, error) {
	data, err := socketAPI(ctx, h.Client.HerdrSocket, "agent.list", map[string]string{})
	if err != nil {
		return nil, err
	}
	var response struct {
		Result struct {
			Agents []notificationAgent `json:"agents"`
		} `json:"result"`
	}
	if err = json.Unmarshal(data, &response); err != nil {
		return nil, err
	}
	return response.Result.Agents, nil
}

func (h *HostOperations) resolveNotification(ctx context.Context, n NativeConversation) (notificationAgent, error) {
	if err := n.validate(); err != nil {
		return notificationAgent{}, err
	}
	agents, err := h.notificationAgents(ctx)
	if err != nil {
		return notificationAgent{}, err
	}
	var matches []notificationAgent
	for _, a := range agents {
		if a.Conversation != nil && *a.Conversation == n && a.Kind == n.Agent {
			matches = append(matches, a)
		}
	}
	if len(matches) == 0 {
		return notificationAgent{}, fmt.Errorf("recipient_missing")
	}
	if len(matches) != 1 {
		return notificationAgent{}, fmt.Errorf("recipient_ambiguous")
	}
	a := matches[0]
	if safeID(a.PaneID) != nil || safeID(a.TerminalID) != nil {
		return notificationAgent{}, fmt.Errorf("native_identity_required")
	}
	data, err := socketAPI(ctx, h.Client.HerdrSocket, "agent.get", map[string]string{"target": a.PaneID})
	if err != nil {
		return notificationAgent{}, err
	}
	var current struct {
		Result struct {
			Agent notificationAgent `json:"agent"`
		} `json:"result"`
	}
	if err = json.Unmarshal(data, &current); err != nil {
		return notificationAgent{}, err
	}
	live := current.Result.Agent
	if live.Conversation == nil || *live.Conversation != n || live.Kind != n.Agent || live.TerminalID != a.TerminalID || live.PaneID != a.PaneID {
		return notificationAgent{}, fmt.Errorf("recipient_replaced")
	}
	return live, nil
}

func (s *Store) notificationDir(name string) (string, error) {
	dir := filepath.Join(s.dir, "notifications")
	if err := privateDir(dir); err != nil {
		return "", err
	}
	dir = filepath.Join(dir, name)
	return dir, privateDir(dir)
}

func (h *HostOperations) notificationBinding(key string) (NotificationBinding, error) {
	var result NotificationBinding
	if !validDigest(key) {
		return result, fmt.Errorf("invalid binding key")
	}
	b, err := h.Store.Binding(key)
	if err != nil {
		return result, err
	}
	if !h.localCleanupTarget(b.Target) || b.JoinPhase != "complete" || b.Status != "online" {
		return result, fmt.Errorf("participant_disconnected")
	}
	dir, err := h.Store.notificationDir("enrollments")
	if err != nil {
		return result, err
	}
	if err = readJSON(filepath.Join(dir, key+".json"), &result); err != nil {
		return result, err
	}
	if result.Key != key || HostBindingKey(result.Binding.RoomID, result.Binding.Target) != key || result.Binding.SessionID != b.SessionID || result.Binding.RoomID != b.RoomID || result.Binding.ParticipantName != b.ParticipantName {
		return result, fmt.Errorf("notification_binding_mismatch")
	}
	result.Binding = b
	return result, result.Conversation.validate()
}

// EnrollNotifications never replaces a conversation captured by an earlier enrollment.
func (h *HostOperations) EnrollNotifications(ctx context.Context, b Binding) error {
	key := HostBindingKey(b.RoomID, b.Target)
	dir, err := h.Store.notificationDir("enrollments")
	if err != nil {
		return err
	}
	return lockFile(ctx, filepath.Join(dir, key+".lock"), func() error {
		if existing, err := h.notificationBinding(key); err == nil {
			if existing.Binding.SessionID != b.SessionID {
				return fmt.Errorf("notification_binding_mismatch")
			}
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		stored, err := h.Store.Binding(key)
		if err != nil {
			return err
		}
		if stored.SessionID != b.SessionID || !h.localCleanupTarget(stored.Target) || stored.JoinPhase != "complete" || stored.Status != "online" {
			return fmt.Errorf("participant_disconnected")
		}
		agents, err := h.notificationAgents(ctx)
		if err != nil {
			return err
		}
		var selected *NativeConversation
		for _, a := range agents {
			if a.TerminalID == b.Target.TerminalID && a.Kind == b.Target.Kind && a.Conversation != nil {
				if selected != nil {
					return fmt.Errorf("recipient_ambiguous")
				}
				n := *a.Conversation
				selected = &n
			}
		}
		if selected == nil {
			return fmt.Errorf("native_identity_required")
		}
		if _, err = h.resolveNotification(ctx, *selected); err != nil {
			return err
		}
		return writeJSON(filepath.Join(dir, key+".json"), NotificationBinding{Key: key, Binding: stored, Conversation: *selected})
	})
}

func (h *HostOperations) NotificationBindings(ctx context.Context) ([]NotificationBinding, error) {
	state, err := h.Store.Read()
	if err != nil {
		return nil, err
	}
	result := []NotificationBinding{}
	for key, b := range state.Bindings {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !h.localCleanupTarget(b.Target) || b.JoinPhase != "complete" || b.Status != "online" {
			continue
		}
		n, err := h.notificationBinding(key)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		result = append(result, n)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key < result[j].Key })
	return result, nil
}

func (h *HostOperations) ObserveNotification(ctx context.Context, key string) (NotificationObservation, error) {
	n, err := h.notificationBinding(key)
	if err != nil {
		return NotificationObservation{}, err
	}
	a, err := h.resolveNotification(ctx, n.Conversation)
	if err != nil {
		return NotificationObservation{}, err
	}
	_, code, err := h.original(ctx, n.Binding)
	if err != nil {
		return NotificationObservation{}, err
	}
	if code != "" {
		return NotificationObservation{}, fmt.Errorf("%s", code)
	}
	p, err := h.Store.pinnedPaths()
	if err != nil {
		return NotificationObservation{}, err
	}
	s, err := parleyRuntime.ParticipantStore(p, n.Binding.RoomID, n.Binding.ParticipantName)
	if err != nil {
		return NotificationObservation{}, err
	}
	meta, err := s.LoadMeta()
	if err != nil {
		return NotificationObservation{}, err
	}
	events, err := s.Inbox(true)
	if err != nil {
		return NotificationObservation{}, err
	}
	o := NotificationObservation{NotificationBinding: n, Agent: a, SeenSeq: meta.LastSeenSeq}
	for _, e := range events {
		if e.Type == model.EventMessage && e.Actor != n.Binding.ParticipantName && e.Seq > o.ThroughSeq {
			o.ThroughSeq = e.Seq
		}
	}
	return o, nil
}

// A moved enrolled conversation must not lose its participant to terminal cleanup.
func (h *HostOperations) notificationRecipientPresent(ctx context.Context, target AgentTarget) (bool, error) {
	bindings, err := h.NotificationBindings(ctx)
	if err != nil {
		return false, err
	}
	for _, n := range bindings {
		if SameTerminal(n.Binding.Target, target) {
			_, err := h.resolveNotification(ctx, n.Conversation)
			if err == nil {
				return true, nil
			}
			if err.Error() != "recipient_missing" {
				return false, err
			}
		}
	}
	return false, nil
}
