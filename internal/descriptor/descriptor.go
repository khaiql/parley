package descriptor

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/khaiql/parley/internal/paths"
)

type Descriptor struct {
	Host   string `json:"host"`
	Port   int    `json:"port"`
	RoomID string `json:"room_id"`
}

func Parse(raw string) (Descriptor, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return Descriptor{}, fmt.Errorf("parse descriptor: %w", err)
	}
	if u.Scheme != "parley" {
		return Descriptor{}, fmt.Errorf("descriptor scheme must be parley")
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") {
		return Descriptor{}, fmt.Errorf("descriptor query and fragment are not supported")
	}
	host := u.Hostname()
	portText := u.Port()
	if host == "" || portText == "" {
		return Descriptor{}, fmt.Errorf("descriptor requires host and port")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port <= 0 || port > 65535 {
		return Descriptor{}, fmt.Errorf("descriptor port is invalid")
	}
	escapedRoomID := strings.TrimPrefix(u.EscapedPath(), "/")
	roomID, err := url.PathUnescape(escapedRoomID)
	if err != nil {
		return Descriptor{}, fmt.Errorf("descriptor room id is invalid: %w", err)
	}
	if err := paths.ValidateRoomID(roomID); err != nil {
		return Descriptor{}, fmt.Errorf("descriptor requires exactly one room id path segment: %w", err)
	}
	return Descriptor{Host: host, Port: port, RoomID: roomID}, nil
}

func (d Descriptor) Addr() string {
	return net.JoinHostPort(d.Host, strconv.Itoa(d.Port))
}

func (d Descriptor) String() string {
	host := d.Host
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return fmt.Sprintf("parley://%s:%d/%s", host, d.Port, url.PathEscape(d.RoomID))
}
