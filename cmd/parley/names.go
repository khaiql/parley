package main

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

var generatedNameAdjectives = []string{
	"brisk",
	"calm",
	"clear",
	"clever",
	"curious",
	"gentle",
	"lively",
	"nimble",
	"patient",
	"quiet",
	"ready",
	"steady",
}

var generatedNameNouns = []string{
	"analyst",
	"builder",
	"coder",
	"helper",
	"pilot",
	"reviewer",
	"runner",
	"scribe",
	"solver",
	"tester",
	"thinker",
	"writer",
}

func generatedParticipantName() (string, error) {
	adjective, err := randomChoice(generatedNameAdjectives)
	if err != nil {
		return "", err
	}
	noun, err := randomChoice(generatedNameNouns)
	if err != nil {
		return "", err
	}
	number, err := randomInt(9000)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s_%s_%04d", adjective, noun, number+1000), nil
}

func randomChoice(values []string) (string, error) {
	if len(values) == 0 {
		return "", fmt.Errorf("empty name word list")
	}
	index, err := randomInt(len(values))
	if err != nil {
		return "", err
	}
	return values[index], nil
}

func randomInt(limit int) (int, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(limit)))
	if err != nil {
		return 0, err
	}
	return int(n.Int64()), nil
}
