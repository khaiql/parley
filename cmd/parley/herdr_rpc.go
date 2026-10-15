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
	if r.Operation != "events" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
	h, e := herdr.ResolveHost(ctx, c, r.Session)
	if e != nil {
		if r.Operation == "events" {
			return nil, eventStartupError(out, e)
		}
		return nil, e
	}
	if r.Operation == "events" {
		if e = h.VerifyController(ctx, r); e != nil {
			return nil, eventStartupError(out, e)
		}
		return nil, herdr.RelayEvents(ctx, h.Config.HerdrSocket, out)
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
