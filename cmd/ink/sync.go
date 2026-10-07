package main

import (
	"encoding/json"
	"strings"

	"github.com/spf13/cobra"
)

func syncCmd() *cobra.Command {
	sync := &cobra.Command{Use: "sync", Short: "Push cards to apps and line apps up against the library"}
	sync.AddCommand(pushCmd())
	return sync
}

func pushCmd() *cobra.Command {
	var newCopy bool
	push := &cobra.Command{
		Use:     "push <instance-id> <card-id>|<query-json>",
		Short:   "Push one card, or the cards a search matches, to an app",
		Example: "  ink sync push 3 12\n  ink sync push 3 '{\"filter\": {\"tag\": \"fantasy\"}}'",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			inst, err := parseID(args[0])
			if err != nil {
				return err
			}
			if strings.HasPrefix(strings.TrimSpace(args[1]), "{") {
				query, err := jsonArg(args, 1, "")
				if err != nil {
					return err
				}
				return startJob(cmd, "/pushes", map[string]any{"instance_id": json.Number(inst), "query": query, "new_copy": newCopy})
			}
			card, err := parseID(args[1])
			if err != nil {
				return err
			}
			return startJob(cmd, "/cards/"+card+"/push", map[string]any{"instance_id": json.Number(inst), "new_copy": newCopy})
		},
	}
	push.Flags().BoolVar(&newCopy, "new-copy", false, "allow another copy in an app that can't update a card")
	return push
}
