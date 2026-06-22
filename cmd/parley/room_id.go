package main

import (
	"crypto/rand"
	"encoding/hex"
)

func newRoomID() (string, error) {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "room-" + hex.EncodeToString(b[:]), nil
}
