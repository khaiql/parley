package room

// State holds the authoritative room state and emits events when it changes.
type State struct {
	subscribers []chan Event
}
