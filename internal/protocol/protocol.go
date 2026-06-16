package protocol

import (
	"bytes"
	"encoding/json"

	"github.com/khaiql/parley/internal/model"
)

type RequestType string

const (
	RequestJoin    RequestType = "join"
	RequestSend    RequestType = "send"
	RequestHistory RequestType = "history"
	RequestLeave   RequestType = "leave"
)

type Request struct {
	Type    RequestType     `json:"type"`
	Join    *JoinRequest    `json:"join,omitempty"`
	Send    *SendRequest    `json:"send,omitempty"`
	History *HistoryRequest `json:"history,omitempty"`
	Leave   *LeaveRequest   `json:"leave,omitempty"`
}

type JoinRequest struct {
	RoomID    string `json:"room_id"`
	Name      string `json:"name"`
	Role      string `json:"role"`
	Directory string `json:"directory,omitempty"`
	Repo      string `json:"repo,omitempty"`
}

type SendRequest struct {
	Text string `json:"text"`
}

type HistoryRequest struct {
	AfterSeq int64 `json:"after_seq,omitempty"`
	Limit    int   `json:"limit,omitempty"`
	All      bool  `json:"all,omitempty"`
}

type LeaveRequest struct {
	Name string `json:"name,omitempty"`
}

type Response struct {
	OK           bool                `json:"ok"`
	Error        *Error              `json:"error,omitempty"`
	Room         *model.RoomMetadata `json:"room,omitempty"`
	Participants []model.Participant `json:"participants,omitempty"`
	Events       []model.Event       `json:"events,omitempty"`
	Event        *model.Event        `json:"event,omitempty"`
	LatestSeq    int64               `json:"latest_seq,omitempty"`
}

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func EncodeLine(v interface{}) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func DecodeLine(data []byte, v interface{}) error {
	return json.Unmarshal(bytes.TrimSpace(data), v)
}
