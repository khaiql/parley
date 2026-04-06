package room

import (
	"encoding/json"

	"github.com/khaiql/parley/internal/protocol"
)

// ParseActivity converts a protocol status string to an Activity enum value.
func ParseActivity(status string) Activity {
	switch {
	case status == protocol.StatusGenerating:
		return ActivityGenerating
	case status == protocol.StatusThinking:
		return ActivityThinking
	case protocol.IsUsingTool(status):
		return ActivityUsingTool
	default:
		return ActivityIdle
	}
}

// HandleServerMessage dispatches an incoming RawMessage, updating internal
// state and emitting the corresponding typed event.
func (s *State) HandleServerMessage(raw *protocol.RawMessage) {
	if raw == nil {
		return
	}

	switch raw.Method {
	case protocol.MethodState:
		var params protocol.RoomStateParams
		if err := json.Unmarshal(raw.Params, &params); err != nil {
			s.emit(ErrorOccurred{Error: err})
			return
		}
		s.roomID = params.RoomID
		s.topic = params.Topic
		s.participants = make([]protocol.Participant, len(params.Participants))
		copy(s.participants, params.Participants)
		s.messages = make([]protocol.MessageParams, len(params.Messages))
		copy(s.messages, params.Messages)
		s.autoApprove = params.AutoApprove

		// Build activities snapshot (all listening initially).
		activities := make(map[string]Activity, len(s.activities))
		for k, v := range s.activities {
			activities[k] = v
		}

		// Emit with fresh copies so consumers own the slices.
		outP := make([]protocol.Participant, len(s.participants))
		copy(outP, s.participants)
		outM := make([]protocol.MessageParams, len(s.messages))
		copy(outM, s.messages)

		s.emit(HistoryLoaded{
			Messages:     outM,
			Participants: outP,
			Activities:   activities,
		})

	case protocol.MethodMessage:
		var params protocol.MessageParams
		if err := json.Unmarshal(raw.Params, &params); err != nil {
			s.emit(ErrorOccurred{Error: err})
			return
		}
		s.messages = append(s.messages, params)
		s.emit(MessageReceived{Message: params})

	case protocol.MethodJoined:
		var params protocol.JoinedParams
		if err := json.Unmarshal(raw.Params, &params); err != nil {
			s.emit(ErrorOccurred{Error: err})
			return
		}
		p := protocol.Participant{
			Name:      params.Name,
			Role:      params.Role,
			Directory: params.Directory,
			Repo:      params.Repo,
			AgentType: params.AgentType,
			Online:    true,
		}
		// Add or replace existing participant with the same name.
		found := false
		for i, existing := range s.participants {
			if existing.Name == p.Name {
				s.participants[i] = p
				found = true
				break
			}
		}
		if !found {
			s.participants = append(s.participants, p)
		}
		out := make([]protocol.Participant, len(s.participants))
		copy(out, s.participants)
		s.emit(ParticipantsChanged{Participants: out})

	case protocol.MethodLeft:
		var params protocol.LeftParams
		if err := json.Unmarshal(raw.Params, &params); err != nil {
			s.emit(ErrorOccurred{Error: err})
			return
		}
		for i, p := range s.participants {
			if p.Name == params.Name {
				s.participants[i].Online = false
				break
			}
		}
		out := make([]protocol.Participant, len(s.participants))
		copy(out, s.participants)
		s.emit(ParticipantsChanged{Participants: out})

	case protocol.MethodStatus:
		var params protocol.StatusParams
		if err := json.Unmarshal(raw.Params, &params); err != nil {
			s.emit(ErrorOccurred{Error: err})
			return
		}
		act := ParseActivity(params.Status)
		s.activities[params.Name] = act
		s.emit(ParticipantActivityChanged{
			Name:     params.Name,
			Activity: act,
		})
	}
}
