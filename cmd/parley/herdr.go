package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/khaiql/parley/internal/herdr"
	"github.com/spf13/cobra"
)

func nativeClient() (*herdr.Client, error) {
	binary := os.Getenv("HERDR_BIN_PATH")
	if binary == "" {
		var err error
		binary, err = exec.LookPath("herdr")
		if err != nil {
			return nil, err
		}
	}
	parley, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return &herdr.Client{HerdrBinary: binary, ParleyBinary: parley}, nil
}
func nativeCommand(use string, hidden bool, run func(*cobra.Command, *herdr.Client, herdr.HostSession, string, []string) error) *cobra.Command {
	return &cobra.Command{Use: use, Hidden: hidden, Args: noArgsJSON, RunE: func(cmd *cobra.Command, args []string) error {
		c, err := nativeClient()
		if err != nil {
			return nativeError(cmd, "helper_unavailable", err)
		}
		if err = c.VerifyVersion(cmd.Context()); err != nil {
			return nativeError(cmd, "helper_incompatible", err)
		}
		h, session, err := herdr.NativeHost(cmd.Context(), c)
		if err != nil {
			return nativeError(cmd, "native_context_required", err)
		}
		if err = run(cmd, c, h, session, args); err != nil {
			return nativeError(cmd, "herdr_failed", err)
		}
		return nil
	}}
}

func nativeError(cmd *cobra.Command, code string, err error) error {
	if (cmd.Name() == "action" || cmd.Name() == "form") && !errors.Is(err, herdr.ErrFormCancelled) {
		// Notification must work even before helper/version/context preflight succeeds.
		c := &herdr.Client{HerdrSocket: os.Getenv("HERDR_SOCKET_PATH")}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(cmd.Context()), 2*time.Second)
		defer cancel()
		if notifyErr := c.Notify(ctx, "Parley failed", nativeErrorHint(code, err)); notifyErr != nil {
			err = fmt.Errorf("%w; notification_failed: %v", err, notifyErr)
		}
	}
	return writeJSONError(cmd, code, err.Error())
}

func nativeErrorHint(code string, err error) string {
	switch {
	case strings.Contains(err.Error(), "agent_not_found"):
		return "Focus an existing agent pane, then use the Parley Start/Join shortcut."
	case strings.Contains(err.Error(), "agent_identity_required"):
		return "Herdr needs a detected agent and terminal ID. Focus an existing supported agent pane and check agent get."
	case strings.Contains(err.Error(), "ui_busy"):
		return "Close the active Herdr modal, then retry the Parley Start/Join shortcut."
	case code == "helper_unavailable":
		return "Herdr helper unavailable. Install Herdr on this host and install/relink the Parley bundle."
	case code == "helper_incompatible":
		return "Herdr 0.9.3 or newer required. Check the Herdr binary on this host."
	case code == "native_context_required":
		return "Herdr context unavailable. Use the Parley shortcut in the intended Herdr agent pane; check this host's plugin setup."
	default:
		return code + ": " + err.Error()
	}
}
func herdrCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "herdr", Short: "Herdr room actions and automatic SSH routing"}
	setup := nativeCommand("setup", false, func(cmd *cobra.Command, c *herdr.Client, h herdr.HostSession, session string, _ []string) error {
		owner, err := h.Setup(cmd.Context(), c)
		if err != nil {
			return err
		}
		if err = h.Startup(cmd.Context(), c.ParleyBinary, session); err != nil {
			return err
		}
		return writeJSON(cmd, owner)
	})
	startup := nativeCommand("startup", false, func(cmd *cobra.Command, c *herdr.Client, h herdr.HostSession, session string, _ []string) error {
		if err := h.Startup(cmd.Context(), c.ParleyBinary, session); err != nil {
			return err
		}
		return writeJSON(cmd, map[string]string{"status": "ready"})
	})
	action := nativeCommand("action <start|join>", false, func(cmd *cobra.Command, c *herdr.Client, h herdr.HostSession, _ string, args []string) error {
		owner, err := h.Owner(cmd.Context())
		if err != nil {
			return err
		}
		c.LocalMachineID = owner.MachineID
		c.HerdrSocket = owner.HerdrSocket
		target, _, err := c.Agent(cmd.Context(), herdr.AgentTarget{PaneID: os.Getenv("HERDR_PANE_ID")})
		if err != nil {
			return err
		}
		if err = h.PinTrustedRuntime(c); err != nil {
			return err
		}
		target.MachineID = "self"
		if err = c.OpenForm(cmd.Context(), args[0], target); err != nil {
			return err
		}
		return writeJSON(cmd, map[string]string{"status": "opened"})
	})
	// The manifest supplies exactly one fixed action argument.
	action.Args = func(_ *cobra.Command, args []string) error {
		if len(args) != 1 || (args[0] != "start" && args[0] != "join") {
			return fmt.Errorf("expected start or join")
		}
		return nil
	}
	form := nativeCommand("form", true, func(cmd *cobra.Command, c *herdr.Client, h herdr.HostSession, _ string, _ []string) error {
		return runForm(cmd, c, h)
	})
	status := nativeCommand("status", false, func(cmd *cobra.Command, _ *herdr.Client, h herdr.HostSession, _ string, _ []string) error {
		state, err := h.Store.Read()
		if err != nil {
			return err
		}
		handoffs, err := h.Store.HandoffStatuses()
		if err != nil {
			return err
		}
		return writeJSON(cmd, struct {
			herdr.State
			Handoffs map[string]herdr.HandoffResult `json:"handoffs"`
		}{state, handoffs})
	})
	broker := &cobra.Command{Use: "broker", Hidden: true, Args: noArgsJSON, RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := nativeClient()
		if err != nil {
			return err
		}
		session, _ := cmd.Flags().GetString("native-session")
		h, err := herdr.ResolveHost(cmd.Context(), c, session)
		if err != nil {
			return err
		}
		ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
		defer stop()
		return h.Coordinate(ctx, c)
	}}
	broker.Flags().String("native-session", "", "verified native session")
	cmd.AddCommand(herdrRPCCmd(), setup, startup, action, form, status, broker)
	return cmd
}
func runForm(cmd *cobra.Command, c *herdr.Client, h herdr.HostSession) error {
	op := os.Getenv("PARLEY_ACTION")
	if op != "start" && op != "join" {
		return fmt.Errorf("invalid form action")
	}
	target, err := herdr.DecodeTarget(os.Getenv("PARLEY_TARGET_JSON"), h.Config.HerdrSocket)
	if err != nil {
		return err
	}
	// Pin trusted host environment before any durable queue write or remote helper access.
	owner, err := h.Owner(cmd.Context())
	if err != nil {
		return err
	}
	c.LocalMachineID = owner.MachineID
	c.HerdrSocket = owner.HerdrSocket
	restore, err := formTerminal(cmd.InOrStdin())
	if err != nil {
		return err
	}
	defer restore()
	ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	defer stop()
	readDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			restore()
			if f, ok := cmd.InOrStdin().(*os.File); ok {
				_ = f.Close()
			}
		case <-readDone:
		}
	}()
	defer close(readDone)
	var value string
	var selectedHost *herdr.RoomHostRef
	var selectedTopic string
	if op == "start" {
		fmt.Fprint(cmd.OutOrStdout(), "Parley start — Topic (Enter submits, Escape cancels):\r\n")
		value, err = herdr.ReadTerminalForm(cmd.InOrStdin(), cmd.OutOrStdout(), op)
	} else {
		value, err = pickRoom(cmd.InOrStdin(), cmd.OutOrStdout(), func() (herdr.RoomCatalog, error) {
			lookup, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			return h.RoomChoices(lookup, c, target)
		}, func(room herdr.Room) error {
			check, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			if err := h.CheckRoomChoice(check, c, target, room); err != nil {
				return err
			}
			host := herdr.RoomHostReference(room, c.LocalMachineID)
			selectedHost = &host
			selectedTopic = room.Topic
			return nil
		})
	}
	if err != nil {
		return err
	}
	restore()
	id, err := herdr.NewID()
	if err != nil {
		return err
	}
	req := herdr.Request{ID: id, Op: op, Target: target, RoomHost: selectedHost, RoomTopic: selectedTopic}
	if op == "start" {
		req.Topic = value
	} else {
		req.RoomID = value
	}
	wait, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	result, err := h.SubmitForm(wait, c, req)
	if err != nil {
		return fmt.Errorf("request %s: %w", id, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "\r\nRequest: %s\r\n", id)
	if result.Room != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "Room: %s\r\n", result.Room.ID)
	}
	if result.Binding != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "Session: %s\r\n", result.Binding.SessionID)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s %s\r\n%s\r\n", result.Status, result.Code, result.Message)
	if result.Binding != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "parley --session %s send \"message\"\r\n", result.Binding.SessionID)
	}
	fmt.Fprint(cmd.OutOrStdout(), "Press Enter to close.\r\n")
	if result.Handoff != nil {
		for {
			fmt.Fprintf(cmd.OutOrStdout(), "Agent instructions: %s. %s\r\n", result.Handoff.Status, result.Handoff.Message)
			if result.Handoff.Status == "failed" {
				fmt.Fprint(cmd.OutOrStdout(), "R + Enter retries instructions only; Enter closes.\r\n")
			}
			value, _ := herdr.ReadForm(cmd.InOrStdin(), "start")
			if result.Handoff.Status != "failed" || strings.ToLower(value) != "r" || result.Binding == nil {
				break
			}
			operations := herdr.HostOperations{Store: h.Store, Client: c}
			retry := operations.Handoff(ctx, *result.Binding, true)
			result.Handoff = &retry
		}
	} else {
		_, _ = herdr.ReadForm(cmd.InOrStdin(), "start")
	}
	if result.Status == "error" {
		return fmt.Errorf("%s: %s", result.Code, result.Message)
	}
	return nil
}
func formTerminal(in io.Reader) (func(), error) {
	f, ok := in.(*os.File)
	if !ok {
		return func() {}, nil
	}
	save := exec.Command("stty", "-g")
	save.Stdin = f
	b, err := save.Output()
	if err != nil {
		return nil, fmt.Errorf("form requires a terminal: %w", err)
	}
	state := strings.TrimSpace(string(b))
	if state == "" || len(state) > 4096 || strings.ContainsAny(state, "\x00\r\n ") {
		return nil, fmt.Errorf("invalid terminal settings")
	}
	raw := exec.Command("stty", "-icanon", "-echo", "min", "1", "time", "0")
	raw.Stdin = f
	if err = raw.Run(); err != nil {
		return nil, err
	}
	var once sync.Once
	return func() {
		once.Do(func() { restore := exec.Command("stty", state); restore.Stdin = f; _ = restore.Run() })
	}, nil
}
