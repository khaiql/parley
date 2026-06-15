package protocol

import (
	"encoding/json"
	"testing"

	"github.com/khaiql/parley/internal/model"
)

func TestEncodeDecodeRequest(t *testing.T) {
	req := Request{
		Type: RequestJoin,
		Join: &JoinRequest{
			RoomID: "room-1",
			Name:   "codex",
			Role:   "reviewer",
		},
	}
	data, err := EncodeLine(req)
	if err != nil {
		t.Fatalf("EncodeLine: %v", err)
	}
	var decoded Request
	if err := DecodeLine(data, &decoded); err != nil {
		t.Fatalf("DecodeLine: %v", err)
	}
	if decoded.Type != RequestJoin || decoded.Join == nil || decoded.Join.Name != "codex" {
		t.Fatalf("decoded = %#v", decoded)
	}
}

func TestEncodeLineAppendsNewline(t *testing.T) {
	data, err := EncodeLine(Request{Type: RequestLeave})
	if err != nil {
		t.Fatalf("EncodeLine: %v", err)
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		t.Fatalf("EncodeLine() = %q, want trailing newline", data)
	}
}

func TestDecodeLineRejectsMultipleValues(t *testing.T) {
	var req Request
	err := DecodeLine([]byte(`{"type":"join"} {"type":"send"}`), &req)
	if err == nil {
		t.Fatal("DecodeLine accepted multiple JSON values")
	}
}

func TestDecodeLineTrimsWhitespace(t *testing.T) {
	var decoded Request
	err := DecodeLine([]byte(" \t\n{\"type\":\"send\",\"send\":{\"text\":\"hello\"}}\r\n "), &decoded)
	if err != nil {
		t.Fatalf("DecodeLine: %v", err)
	}
	if decoded.Type != RequestSend || decoded.Send == nil || decoded.Send.Text != "hello" {
		t.Fatalf("decoded = %#v", decoded)
	}
}

func TestRequestPayloadRoundTrips(t *testing.T) {
	tests := []struct {
		name  string
		req   Request
		check func(t *testing.T, got Request)
	}{
		{
			name: "send",
			req: Request{
				Type: RequestSend,
				Send: &SendRequest{Text: "ship it"},
			},
			check: func(t *testing.T, got Request) {
				if got.Type != RequestSend || got.Send == nil || got.Send.Text != "ship it" {
					t.Fatalf("got = %#v", got)
				}
			},
		},
		{
			name: "history",
			req: Request{
				Type:    RequestHistory,
				History: &HistoryRequest{AfterSeq: 7, Limit: 20, All: true},
			},
			check: func(t *testing.T, got Request) {
				if got.Type != RequestHistory || got.History == nil {
					t.Fatalf("got = %#v", got)
				}
				if got.History.AfterSeq != 7 || got.History.Limit != 20 || !got.History.All {
					t.Fatalf("history = %#v", got.History)
				}
			},
		},
		{
			name: "leave",
			req: Request{
				Type:  RequestLeave,
				Leave: &LeaveRequest{Name: "codex"},
			},
			check: func(t *testing.T, got Request) {
				if got.Type != RequestLeave || got.Leave == nil || got.Leave.Name != "codex" {
					t.Fatalf("got = %#v", got)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := EncodeLine(tt.req)
			if err != nil {
				t.Fatalf("EncodeLine: %v", err)
			}
			var got Request
			if err := DecodeLine(data, &got); err != nil {
				t.Fatalf("DecodeLine: %v", err)
			}
			tt.check(t, got)
		})
	}
}

func TestResponseCarriesEvent(t *testing.T) {
	resp := Response{
		OK:    true,
		Event: &model.Event{Seq: 1, Type: model.EventMessage, RoomID: "room-1"},
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var wire struct {
		OK    bool `json:"ok"`
		Event struct {
			Seq    int64           `json:"seq"`
			Type   model.EventType `json:"type"`
			RoomID string          `json:"room_id"`
		} `json:"event"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !wire.OK || wire.Event.Seq != 1 || wire.Event.Type != model.EventMessage || wire.Event.RoomID != "room-1" {
		t.Fatalf("wire = %#v", wire)
	}
}

func TestResponseMetadataRoundTrips(t *testing.T) {
	resp := Response{
		OK:        false,
		Error:     &Error{Code: "bad_request", Message: "missing room"},
		Events:    []model.Event{{Seq: 4, Type: model.EventMessage, RoomID: "room-1"}},
		LatestSeq: 4,
	}
	data, err := EncodeLine(resp)
	if err != nil {
		t.Fatalf("EncodeLine: %v", err)
	}
	var decoded Response
	if err := DecodeLine(data, &decoded); err != nil {
		t.Fatalf("DecodeLine: %v", err)
	}
	if decoded.OK || decoded.Error == nil || decoded.Error.Code != "bad_request" {
		t.Fatalf("decoded error response = %#v", decoded)
	}
	if len(decoded.Events) != 1 || decoded.Events[0].Seq != 4 || decoded.LatestSeq != 4 {
		t.Fatalf("decoded history response = %#v", decoded)
	}
}
