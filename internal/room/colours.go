package room

import (
	"crypto/rand"
	"math/big"
)

// AgentPalette is the set of colours available for agent participants.
var AgentPalette = []string{
	"#a78bfa", // purple
	"#7dd3fc", // cyan
	"#34d399", // emerald
	"#fbbf24", // amber
	"#f472b6", // pink
	"#60a5fa", // blue
	"#a3e635", // lime
	"#fb923c", // orange
}

// AssignColour picks a random colour from AgentPalette that is not in the used
// set. If all colours are taken, it picks a random one from the full palette
// (graceful degradation for 9+ participants).
func AssignColour(used []string) string {
	usedSet := make(map[string]bool, len(used))
	for _, c := range used {
		usedSet[c] = true
	}

	var available []string
	for _, c := range AgentPalette {
		if !usedSet[c] {
			available = append(available, c)
		}
	}

	if len(available) == 0 {
		// Fallback: all colours taken, pick randomly from full palette.
		available = AgentPalette
	}

	n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(available))))
	return available[n.Int64()]
}
