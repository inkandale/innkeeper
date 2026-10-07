package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

// jsonArg returns args[i] if it is valid JSON, or def if args has no index i.
func jsonArg(args []string, i int, def string) (json.RawMessage, error) {
	if len(args) <= i {
		return json.RawMessage(def), nil
	}
	if !json.Valid([]byte(args[i])) {
		return nil, fmt.Errorf("%q is not valid JSON", args[i])
	}
	return json.RawMessage(args[i]), nil
}

func jsonFlag(cmd *cobra.Command) bool {
	v, _ := cmd.Flags().GetBool("json")
	return v
}

// show prints an API response as it came with --json, and otherwise decodes
// it and prints it with table.
func show[T any](cmd *cobra.Command, raw []byte, table func(io.Writer, T) error) error {
	w := cmd.OutOrStdout()
	if jsonFlag(cmd) {
		_, err := fmt.Fprintln(w, strings.TrimSuffix(string(raw), "\n"))
		return err
	}
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	return table(w, v)
}

func callCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "call <method> <path> [json]",
		Short:   "Make any API call and print the response",
		Long:    "Call reaches API routes that have no command of their own. The path is under /api; the API docs are at /api/docs on the server.",
		Example: "  ink call GET /jobs\n  ink call PATCH /settings '{\"theme\": \"dusk\"}'",
		Args:    cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := jsonArg(args, 2, "")
			if err != nil {
				return err
			}
			if len(body) == 0 {
				body = nil
			}
			c, err := newClient()
			if err != nil {
				return err
			}
			out, err := c.do(cmd.Context(), strings.ToUpper(args[0]), args[1], body)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), strings.TrimSuffix(string(out), "\n"))
			return err
		},
	}
}
