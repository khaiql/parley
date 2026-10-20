// The e2e agent records real terminal input and renders deterministic lifecycle UI.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

func main() {
	if len(os.Args) != 3 {
		panic("usage: codex INPUT_LOG STATE_FILE")
	}
	f, err := os.OpenFile(os.Args[1], os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	var output sync.Mutex
	done := make(chan struct{})
	defer close(done)
	go func() {
		previous := ""
		tick := time.NewTicker(50 * time.Millisecond)
		defer tick.Stop()
		for {
			state, _ := os.ReadFile(os.Args[2])
			if string(state) != previous {
				previous = string(state)
				title := "✳ Claude"
				switch previous {
				case "working":
					title = "⠋ Working"
				case "blocked":
					title = "Action Required"
				}
				output.Lock()
				fmt.Printf("\x1b]0;%s\x07\x1b[2J\x1b[H", title)
				if previous == "unknown" {
					fmt.Print("Unclassified agent screen")
				} else if previous == "blocked" {
					fmt.Print("Do you want to proceed?\n❯ Yes\nEnter to confirm · Esc to cancel")
				} else {
					fmt.Print("Claude Code e2e recorder\n\n──────────────────\n❯ \n──────────────────")
				}
				output.Unlock()
			}
			select {
			case <-done:
				return
			case <-tick.C:
			}
		}
	}()
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		if err := json.NewEncoder(f).Encode(map[string]any{"at": time.Now().UnixNano(), "text": scanner.Text()}); err != nil {
			panic(err)
		}
		if err := f.Sync(); err != nil {
			panic(err)
		}
	}
	if err := scanner.Err(); err != nil {
		panic(err)
	}
}
