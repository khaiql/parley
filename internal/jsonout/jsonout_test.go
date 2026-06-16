package jsonout

import (
	"encoding/json"
	"testing"
)

func TestErrorEnvelope(t *testing.T) {
	data, err := MarshalError("adapter_not_running", "No adapter is running")
	if err != nil {
		t.Fatalf("MarshalError: %v", err)
	}
	var out struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Error.Code != "adapter_not_running" {
		t.Fatalf("code = %q", out.Error.Code)
	}
}
