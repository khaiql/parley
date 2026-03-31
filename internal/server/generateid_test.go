package server

import (
	"sync"
	"testing"
)

func TestGenerateIDUniqueness(t *testing.T) {
	const goroutines = 10
	const idsPerGoroutine = 1000

	var ids sync.Map
	var wg sync.WaitGroup
	duplicates := make(chan string, goroutines*idsPerGoroutine)

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < idsPerGoroutine; i++ {
				id := generateID()
				if _, loaded := ids.LoadOrStore(id, struct{}{}); loaded {
					duplicates <- id
				}
			}
		}()
	}

	wg.Wait()
	close(duplicates)

	var dups []string
	for id := range duplicates {
		dups = append(dups, id)
	}

	if len(dups) > 0 {
		t.Errorf("generateID() produced %d duplicate IDs: first duplicate = %q", len(dups), dups[0])
	}
}
