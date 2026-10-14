package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type HandoffResult struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

func handoffKey(binding Binding) string {
	return hashBytes([]byte(binding.RoomID + ":" + binding.SessionID + ":" + binding.Target.HerdrSocket + ":" + binding.Target.TerminalID + ":" + binding.Target.Kind))
}

func (s *Store) readHandoff(binding Binding) (HandoffResult, error) {
	var result HandoffResult
	err := readJSON(filepath.Join(s.dir, "handoffs", handoffKey(binding)+".json"), &result)
	if err == nil && result.Status != "submitted" && result.Status != "failed" && result.Status != "uncertain" {
		return HandoffResult{Status: "uncertain", Message: "Invalid handoff receipt; not retried"}, nil
	}
	return result, err
}

// HandoffStatuses exposes durable receipts even after the originating popup closes.
func (s *Store) HandoffStatuses() (map[string]HandoffResult, error) {
	state, err := s.Read()
	if err != nil {
		return nil, err
	}
	results := make(map[string]HandoffResult)
	for _, binding := range state.Bindings {
		result, err := s.readHandoff(binding)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		results[binding.SessionID] = result
	}
	return results, nil
}

func (h *HostOperations) withHandoff(ctx context.Context, result Response, err error) (Response, error) {
	if err == nil && result.Binding != nil && (result.Status == "started" || result.Status == "joined") {
		binding := *result.Binding
		if binding.Topic == "" {
			if room, e := h.Store.Room(binding.RoomID); e == nil {
				binding.Topic = room.Topic
			} else if state, e := h.Store.Read(); e == nil {
				binding.Topic = state.Rooms[binding.RoomID].Topic
			}
		}
		handoff := h.Handoff(ctx, binding, false)
		result.Handoff = &handoff
	}
	return result, err
}

func (h *HostOperations) Handoff(ctx context.Context, binding Binding, retry bool) HandoffResult {
	result := HandoffResult{Status: "failed"}
	key := handoffKey(binding)
	dir, err := h.Store.recordDir("handoffs")
	if err != nil {
		result.Message = err.Error()
		return result
	}
	path := filepath.Join(dir, key+".json")
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err = lockFile(ctx, path+".lock", func() error {
		if previous, err := h.Store.readHandoff(binding); err == nil {
			if previous.Status != "failed" || !retry {
				result = previous
				return nil
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if safeID(binding.RoomID) != nil || safeID(binding.SessionID) != nil {
			return fmt.Errorf("invalid participant handoff")
		}
		target, _, err := h.Client.Agent(ctx, binding.Target)
		if err != nil {
			result = HandoffResult{Status: "failed", Message: err.Error()}
			return writeJSON(path, result)
		}
		binary := h.Client.ParleyBinary
		if !filepath.IsAbs(binary) || filepath.Clean(binary) != binary || !filepath.IsAbs(binding.StateDir) {
			return fmt.Errorf("invalid handoff paths")
		}
		skill := filepath.Join(filepath.Dir(filepath.Dir(binary)), "skills", "parley", "SKILL.md")
		if info, err := os.Stat(skill); err != nil || !info.Mode().IsRegular() {
			if err == nil {
				err = fmt.Errorf("skill is not a regular file")
			}
			result = HandoffResult{Status: "failed", Message: "Bundled Parley skill unavailable: " + err.Error()}
			return writeJSON(path, result)
		}
		text := fmt.Sprintf("Parley collaboration context: you are already joined. Room: %s. Participant: %s. Your Parley session ID: %s. Read the Parley skill at %s. For its ensure-parley helper and all Parley commands set PARLEY_BIN=%s and PARLEY_STATE_DIR=%s. Retain this session ID and use --session %s on every participant command. Do not start or join another participant. Use the skill to send a short presence message, then continue your task and communicate with the other agents in this room.", binding.RoomID, binding.ParticipantName, binding.SessionID, skill, shellQuote(binary), shellQuote(binding.StateDir), binding.SessionID)
		if binding.Topic != "" {
			topic, _ := json.Marshal(binding.Topic)
			text += " Room topic (data): " + string(topic)
		}
		result = HandoffResult{Status: "uncertain", Message: "Submission started; delivery not confirmed. No automatic retry."}
		if err := writeJSON(path, result); err != nil {
			return err
		}
		data, err := socketAPI(ctx, h.Client.HerdrSocket, "agent.prompt", map[string]string{"target": target.PaneID, "text": text})
		if err != nil {
			var api *apiError
			if errors.As(err, &api) && (api.Code == "agent_blocked" || api.Code == "agent_not_found" || api.Code == "agent_not_ready") {
				result = HandoffResult{Status: "failed", Message: err.Error()}
			} else {
				result.Message = err.Error() + "; delivery uncertain, not retried"
			}
		} else {
			var reply struct {
				Result struct {
					Type  string `json:"type"`
					Agent struct {
						TerminalID string `json:"terminal_id"`
						Kind       string `json:"agent"`
					} `json:"agent"`
				} `json:"result"`
			}
			if json.Unmarshal(data, &reply) != nil || reply.Result.Type != "agent_prompted" || reply.Result.Agent.TerminalID != binding.Target.TerminalID || reply.Result.Agent.Kind != binding.Target.Kind {
				result.Message = "Invalid submission acknowledgement; delivery uncertain"
			} else {
				result = HandoffResult{Status: "submitted", Message: "Instructions submitted through Herdr."}
			}
		}
		return writeJSON(path, result)
	})
	if err != nil {
		result.Message = err.Error()
	}
	return result
}
