package main

import (
	"context"
	"encoding/json"
	"io"
	"time"

	"github.com/khaiql/parley/internal/herdr"
	"github.com/spf13/cobra"
)

func runHerdrRPC(ctx context.Context, in io.Reader, out io.Writer, c *herdr.Client) (any, error) {
	r, e := herdr.DecodeRPC(in)
	if e != nil {
		return nil, e
	}
	stream := r.Operation == "events" || r.Operation == "notification-events"
	if !stream {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
	h, e := herdr.ResolveHost(ctx, c, r.Session)
	if e != nil {
		if stream {
			return nil, eventStartupError(out, e)
		}
		return nil, e
	}
	if stream {
		if e = h.VerifyController(ctx, r); e != nil {
			return nil, eventStartupError(out, e)
		}
		if r.Operation == "events" {
			return nil, herdr.RelayEvents(ctx, h.Config.HerdrSocket, out)
		}
		owner, err := h.Owner(ctx)
		if err != nil {
			return nil, eventStartupError(out, err)
		}
		c.LocalMachineID = owner.MachineID
		c.HerdrSocket = h.Config.HerdrSocket
		return nil, (&herdr.HostOperations{Store: h.Store, Client: c}).RelayNotificationEvents(ctx, out)
	}
	return h.Dispatch(ctx, r)
}
func herdrRPCCmd() *cobra.Command {
	return &cobra.Command{Use: "rpc", Hidden: true, Args: noArgsJSON, RunE: func(cmd *cobra.Command, _ []string) error {
		c, e := nativeClient()
		if e != nil {
			return writeJSONError(cmd, "helper_unavailable", e.Error())
		}
		r, e := runHerdrRPC(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), c)
		if e != nil {
			return writeJSONError(cmd, herdr.EventFailureCode(e), e.Error())
		}
		if r != nil {
			return writeJSON(cmd, r)
		}
		return nil
	}}
}

func eventStartupError(out io.Writer, cause error) error {
	frame := map[string]any{"id": "parley-requests", "error": map[string]string{"code": herdr.EventFailureCode(cause), "message": cause.Error()}}
	if e := json.NewEncoder(out).Encode(frame); e != nil {
		return e
	}
	return cause
}
