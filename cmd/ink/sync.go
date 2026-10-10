package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/davidgibbons/innkeeper/internal/wire"
)

func syncCmd() *cobra.Command {
	sync := &cobra.Command{Use: "sync", Short: "Push cards to apps and line apps up against the library"}
	sync.AddCommand(pushCmd(), reconcileCmd(), compareCmd(), resolveCmd(), applyCmd(),
		bindingsCmd(), statusCmd(), previewCmd(), adoptCmd(), unbindCmd())
	return sync
}

func pushCmd() *cobra.Command {
	var newCopy, yes bool
	push := &cobra.Command{
		Use:   "push <instance-id> <card-id>|<query-json>",
		Short: "Push one card, or the cards a search matches, to an app",
		Long: `Push replaces the app's copy, so it overwrites changes made in the app. It
prints what it would replace and asks first; --yes skips the question. A card
push prints each field it changes, and is refused if the app's copy changes
before it runs. A query push prints how many cards are new, ahead, in sync, or
drifted there, as of their last check. A query needs a filter or text.`,
		Example: "  ink sync push 3 12\n  ink sync push 3 '{\"filter\": {\"tag\": \"fantasy\"}}' --yes",
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
				if err := askBulkPush(cmd, inst, query, yes); err != nil {
					return err
				}
				return startJob(cmd, "/pushes", map[string]any{"instance_id": json.Number(inst), "query": query, "new_copy": newCopy})
			}
			card, err := parseID(args[1])
			if err != nil {
				return err
			}
			out, err := apiCall(cmd, "GET", "/cards/"+card+"/push-preview?instance_id="+inst, nil)
			if err != nil {
				return err
			}
			var p wire.Preview
			if err := json.Unmarshal(out, &p); err != nil {
				return err
			}
			if err := printPreview(cmd.ErrOrStderr(), p); err != nil {
				return err
			}
			if p.Replaces && len(p.Changes) > 0 {
				if err := confirm(cmd, yes, "Replace card "+card+"'s copy in the app?"); err != nil {
					return err
				}
			}
			return startJob(cmd, "/cards/"+card+"/push",
				map[string]any{"instance_id": json.Number(inst), "new_copy": newCopy, "confirm": p.Confirm})
		},
	}
	push.Flags().BoolVar(&newCopy, "new-copy", false, "allow another copy in an app that can't update a card")
	push.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask before replacing the app's copies")
	return push
}

// askBulkPush prints how the cards a query matches stand on the instance,
// and asks if the push would replace any copy there.
func askBulkPush(cmd *cobra.Command, inst string, query json.RawMessage, yes bool) error {
	var search map[string]any
	if err := json.Unmarshal(query, &search); err != nil || search == nil {
		return errors.New("the query must be a JSON object")
	}
	if search["filter"] == nil && search["text"] == nil {
		return errors.New("the query needs a filter or text; to select every card, filter on archived")
	}
	search["facets"] = []string{"targets"}
	search["page"] = map[string]int{"size": 1}
	body, err := json.Marshal(search)
	if err != nil {
		return err
	}
	out, err := apiCall(cmd, "POST", "/search", body)
	if err != nil {
		return err
	}
	var res struct {
		Total  int `json:"total"`
		Facets struct {
			Targets []struct {
				Instance json.Number `json:"instance"`
				Is       string      `json:"is"`
				Cards    int         `json:"cards"`
			} `json:"targets"`
		} `json:"facets"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		return err
	}
	if res.Total == 0 {
		return errors.New("the query matches no cards")
	}
	n := map[string]int{}
	bound := 0
	for _, t := range res.Facets.Targets {
		if t.Instance.String() != inst {
			continue
		}
		if t.Is == "remote_changed" || t.Is == "diverged" {
			t.Is = "drifted"
		}
		n[t.Is] += t.Cards
		bound += t.Cards
	}
	var parts []string
	for _, c := range []struct {
		n    int
		what string
	}{
		{res.Total - bound + n["missing"], "new"},
		{n["ahead"], "ahead"},
		{n["in_sync"], "in sync"},
		{n["cant_check"], "can't check"},
		{n["drifted"], "drifted (pushing overwrites their app edits)"},
	} {
		if c.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", c.n, c.what))
		}
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "%d cards: %s\n", res.Total, strings.Join(parts, ", "))
	if replaced := bound - n["missing"]; replaced > 0 {
		return confirm(cmd, yes, fmt.Sprintf("Replace %d copies in the app?", replaced))
	}
	return nil
}
