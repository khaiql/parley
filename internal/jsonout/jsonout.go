package jsonout

import "encoding/json"

type ErrorEnvelope struct {
	Status string    `json:"status"`
	Error  ErrorBody `json:"error"`
}

type ErrorBody struct {
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Details interface{} `json:"details,omitempty"`
}

func Marshal(v interface{}) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}

func MarshalError(code, message string) ([]byte, error) {
	return Marshal(ErrorEnvelope{Status: "error", Error: ErrorBody{Code: code, Message: message}})
}
