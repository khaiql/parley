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
	if decoded.Type != RequestJoin || decoded.Join.Name != "codex" {
		t.Fatalf("decoded = %#v", decoded)
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
	if len(data) == 0 {
		t.Fatal("expected JSON")
	}
}
