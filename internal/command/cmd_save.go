package command

import "fmt"

// SaveCommand triggers an immediate save of room state to disk.
var SaveCommand = &Command{
	Name:        "save",
	Usage:       "/save",
	Description: "Save room state to disk",
	Execute: func(ctx Context, _ string) Result {
		if ctx.SaveFn == nil {
			return Result{Error: fmt.Errorf("save not available")}
		}
		if err := ctx.SaveFn(); err != nil {
			return Result{Error: fmt.Errorf("save failed: %w", err)}
		}
		return Result{LocalMessage: "Room state saved."}
	},
}
