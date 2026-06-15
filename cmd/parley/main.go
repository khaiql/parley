package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/khaiql/parley/internal/jsonout"
)

const (
	version         = "dev"
	protocolVersion = "v1"
)

type cliError struct {
	code    string
	message string
}

func (e cliError) Error() string {
	return e.message
}

func main() {
	if err := execute(newRootCmd()); err != nil {
		os.Exit(1)
	}
}

func execute(cmd *cobra.Command) error {
	err := cmd.Execute()
	if err == nil {
		return nil
	}
	var jsonErr cliError
	if errors.As(err, &jsonErr) {
		return err
	}
	return writeJSONError(cmd, "invalid_arguments", err.Error())
}

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "parley",
		Short:         "JSON-only Parley room CLI",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	cmd.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return writeJSONError(cmd, "invalid_arguments", err.Error())
	})

	cmd.AddCommand(
		startCmd(),
		joinCmd(),
		inviteCmd(),
		infoCmd(),
		statusCmd(),
		inboxCmd(),
		historyCmd(),
		waitCmd(),
		sendCmd(),
		leaveCmd(),
		stopCmd(),
		versionCmd(),
	)

	return cmd
}

func noArgsJSON(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	return writeJSONError(cmd, "invalid_arguments", fmt.Sprintf("%s accepts no arguments", cmd.CommandPath()))
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print Parley version",
		Args:  noArgsJSON,
		RunE: func(cmd *cobra.Command, args []string) error {
			return writeJSON(cmd, struct {
				Version         string `json:"version"`
				ProtocolVersion string `json:"protocol_version"`
			}{
				Version:         version,
				ProtocolVersion: protocolVersion,
			})
		},
	}
}

func writeJSON(cmd *cobra.Command, v interface{}) error {
	out, err := jsonout.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), string(out))
	return err
}

func writeJSONError(cmd *cobra.Command, code, message string) error {
	out, marshalErr := jsonout.MarshalError(code, message)
	if marshalErr != nil {
		return marshalErr
	}
	if _, err := fmt.Fprintln(cmd.OutOrStderr(), string(out)); err != nil {
		return err
	}
	return cliError{code: code, message: message}
}

func notImplemented(cmd *cobra.Command, name string) error {
	return writeJSONError(cmd, "not_implemented", fmt.Sprintf("%s runtime is not implemented yet", name))
}
