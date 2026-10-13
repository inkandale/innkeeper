package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/davidgibbons/innkeeper/internal/wire"
)

func syncCmd() *cobra.Command {
	sync := &cobra.Command{Use: "sync", Short: "Push cards to apps and line apps up against the library"}
	sync.AddCommand(pushCmd(), reconcileCmd(), compareCmd(), resolveCmd(), applyCmd(),
		bindingsCmd(), statusCmd(), previewCmd(), unbindCmd(), refreshCmd(), autosyncCmd(), changesCmd())
	return sync
}

func refreshCmd() *cobra.Command {
	return &cobra.Command{Use: "refresh", Short: "Check every app for changes; changes nothing", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out, err := apiCall(cmd, "POST", "/scans", nil)
			if err != nil {
				return err
			}
			return jobStarted(cmd, out)
		}}
}

func changesCmd() *cobra.Command {
	var app int64
	var state string
	changes := &cobra.Command{
		Use:   "changes",
		Short: "List the cards an app needs you for",
		Long: `Changes prints a line per card with a change in some app, and first the app
items no card matches. It reads each app's latest sync and the binding
statuses. The states are differs, both_changed, ahead, missing, error, and
new; it prints them from the library's side, so ahead is "not pushed" and
both_changed "changed in both". A card not pushed may have been bound to an app
copy that differs, so review it before pushing: ink sync compare --two-way.`,
		Example: "  ink sync changes\n  ink sync changes --app 3 --state both_changed",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			q := url.Values{}
			if app != 0 {
				q.Set("instance_id", fmt.Sprint(app))
			}
			if state != "" {
				q.Set("state", state)
			}
			path := "/sync/changes"
			if len(q) > 0 {
				path += "?" + q.Encode()
			}
			out, err := apiCall(cmd, "GET", path, nil)
			if err != nil {
				return err
			}
			return show(cmd, out, printQueue)
		},
	}
	changes.Flags().Int64Var(&app, "app", 0, "only this app instance's changes")
	changes.Flags().StringVar(&state, "state", "", "only this state")
	return changes
}

// stateLabel names a binding or queue state from the library's side.
func stateLabel(s string) string {
	switch s {
	case "ahead":
		return "not pushed"
	case "remote_changed":
		return "changed in app"
	case "diverged", "both_changed":
		return "changed in both"
	}
	return strings.ReplaceAll(s, "_", " ")
}

// printQueue prints a line per row: its name, then each app's state.
func printQueue(w io.Writer, ch wire.Changes) error {
	for _, r := range ch.Rows {
		apps := make([]string, len(r.Apps))
		for i, a := range r.Apps {
			apps[i] = fmt.Sprintf("#%d: %s", a.InstanceID, stateLabel(a.State))
		}
		if _, err := fmt.Fprintf(w, "%s  %s\n", r.Name, strings.Join(apps, ", ")); err != nil {
			return err
		}
	}
	return nil
}

func pushCmd() *cobra.Command {
	var newCopy, yes bool
	push := &cobra.Command{
		Use:   "push <instance-id> <card-id>|<query-json>",
		Short: "Push one card, or the cards a search matches, to an app",
		Long: `Push replaces the app's copy, so it overwrites changes made in the app. It
prints what it would replace and asks first; --yes skips the question. A card
push prints each field it changes, and is refused if the app's copy changes
before it runs. A query push prints how many cards are new, not pushed, in sync, or
drifted there, as of their last check. It replaces only the copies it counted:
a card whose app copy changes after that fails, and the app keeps its copy. A
query needs a filter or text.`,
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
				token, err := askBulkPush(cmd, inst, query, yes)
				if err != nil {
					return err
				}
				return startJob(cmd, "/pushes",
					map[string]any{"instance_id": json.Number(inst), "query": query, "new_copy": newCopy, "confirm": token})
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
// asks if the push would replace any copy there, and returns the preview's
// confirm.
func askBulkPush(cmd *cobra.Command, inst string, query json.RawMessage, yes bool) (string, error) {
	var search map[string]any
	if err := json.Unmarshal(query, &search); err != nil || search == nil {
		return "", errors.New("the query must be a JSON object")
	}
	if search["filter"] == nil && search["text"] == nil {
		return "", errors.New("the query needs a filter or text; to select every card, filter on archived")
	}
	body, err := json.Marshal(map[string]any{"instance_id": json.Number(inst), "query": query})
	if err != nil {
		return "", err
	}
	out, err := apiCall(cmd, "POST", "/pushes/preview", body)
	if err != nil {
		return "", err
	}
	var p wire.BulkPreview
	if err := json.Unmarshal(out, &p); err != nil {
		return "", err
	}
	if p.Total == 0 {
		return "", errors.New("the query matches no cards")
	}
	var parts []string
	for _, c := range []struct {
		n    int
		what string
	}{
		{p.New, "new"},
		{p.Ahead, "not pushed"},
		{p.InSync, "in sync"},
		{p.CantCheck, "can't check"},
		{p.Drifted, "drifted (pushing overwrites their app edits)"},
	} {
		if c.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", c.n, c.what))
		}
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "%d cards: %s\n", p.Total, strings.Join(parts, ", "))
	if replaced := p.Total - p.New; replaced > 0 {
		return p.Confirm, confirm(cmd, yes, fmt.Sprintf("Replace %d copies in the app?", replaced))
	}
	return p.Confirm, nil
}
