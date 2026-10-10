package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/davidgibbons/innkeeper/internal/merge"
	"github.com/davidgibbons/innkeeper/internal/wire"
)

func reconcileCmd() *cobra.Command {
	var dryRun bool
	run := &cobra.Command{
		Use:     "run <instance-id> [params-json]",
		Aliases: []string{"reconcile"},
		Short:   "Sync an app with the library",
		Long: `Sync reads the app, links exact matches, imports items the library doesn't
have, waits for the job, and prints a row per item with its verdict and
suggested resolution. Items marked (auto) were applied by the sync. Apply the
suggestions for the rest with: ink sync apply <sync-job-id>`,
		Example: "  ink sync run 3\n  ink sync run 3 --dry-run\n  ink sync run 3 '{\"match\": {\"text_distance\": 4}}'",
		Args:    cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			inst, err := parseID(args[0])
			if err != nil {
				return err
			}
			raw, err := jsonArg(args, 1, `{}`)
			if err != nil {
				return err
			}
			var params map[string]any
			if err := json.Unmarshal(raw, &params); err != nil {
				return fmt.Errorf("params: %w", err)
			}
			if params == nil {
				params = map[string]any{}
			}
			params["instance_id"] = json.Number(inst)
			if dryRun {
				params["apply"] = wire.ApplyNone
			}
			c, err := newClient(cmd)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			body, err := json.Marshal(params)
			if err != nil {
				return err
			}
			out, err := c.do(ctx, "POST", "/reconciles", body)
			if err != nil {
				return err
			}
			var j job
			if err := json.Unmarshal(out, &j); err != nil {
				return err
			}
			id := fmt.Sprint(j.ID)
			if err := watchJob(ctx, c, id, cmd.ErrOrStderr()); err != nil {
				return err
			}
			snap, err := snapshot(ctx, c, id)
			if err != nil {
				return err
			}
			if jsonFlag(cmd) {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(snap)
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "sync job %d; apply its suggestions with: ink sync apply %d\n", j.ID, j.ID)
			return printSnapshot(cmd.OutOrStdout(), snap)
		},
	}
	run.Flags().BoolVar(&dryRun, "dry-run", false, "only report what a sync would do; link and import nothing")
	return run
}

func compareCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "compare <instance-id> <remote-id> [card-id]",
		Short: "Show how an app character differs from a card",
		Long: `Compare prints each hunk that differs, with changed words marked [-library-]
and {+app+}. Without a card ID, it compares the card the character is bound to.`,
		Args: cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			inst, err := parseID(args[0])
			if err != nil {
				return err
			}
			card := ""
			if len(args) == 3 {
				if card, err = parseID(args[2]); err != nil {
					return err
				}
			}
			c, err := newClient(cmd)
			if err != nil {
				return err
			}
			cmp, err := fetchComparison(cmd.Context(), c, comparePath(inst, args[1], card))
			if err != nil {
				return err
			}
			if jsonFlag(cmd) {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(cmp)
			}
			printComparison(cmd.OutOrStdout(), cmp)
			return nil
		},
	}
}

func resolveCmd() *cobra.Command {
	var def string
	resolve := &cobra.Command{
		Use:   "resolve <instance-id> <remote-id> bind|import|skip [card-id] [hunk=choice ...]",
		Short: "Bind, import, or skip one app character",
		Long: `Bind and import compare the item first, and the resolution fails if either
side changed since. hunk=choice picks a side for one hunk, using the hunk IDs
compare prints; --default picks it for the rest. Choices are library, app,
keep_in_app, and both (list hunks only).`,
		Example: "  ink sync resolve 3 brakka.png bind 12 first_mes#0=app --default library\n  ink sync resolve 3 brakka.png skip",
		Args:    cobra.MinimumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := wire.Resolution{Choices: map[string]merge.Choice{}, Default: merge.Choice(def)}
			// The first three are always positional, so a remote ID may contain "=".
			pos := slices.Clone(args[:3])
			for _, a := range args[3:] {
				if hunk, choice, ok := strings.Cut(a, "="); ok {
					r.Choices[hunk] = merge.Choice(choice)
				} else {
					pos = append(pos, a)
				}
			}
			if len(pos) < 3 || len(pos) > 4 {
				return fmt.Errorf("want <instance-id> <remote-id> <action> [card-id], got %q", pos)
			}
			inst, err := parseID(pos[0])
			if err != nil {
				return err
			}
			r.RemoteID, r.Action = pos[1], pos[2]
			card := ""
			if len(pos) == 4 {
				if card, err = parseID(pos[3]); err != nil {
					return err
				}
			}
			c, err := newClient(cmd)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			if r.Action != "skip" {
				cmp, err := fetchComparison(ctx, c, comparePath(inst, r.RemoteID, card))
				if err != nil {
					if r.Action == "import" && card == "" {
						return fmt.Errorf("%w; an unbound item has no comparison, so import it from its sync: ink sync apply <sync-job-id> %s", err, r.RemoteID)
					}
					return err
				}
				r.CardID, r.AppHash, r.BaseRevisionID, r.AcceptedHash = cmp.CardID, cmp.AppHash, cmp.HeadRevisionID, cmp.AcceptedHash
				r.BindingID = cmp.BindingID
			}
			body, err := json.Marshal(r)
			if err != nil {
				return err
			}
			out, err := c.do(ctx, "POST", "/instances/"+inst+"/items/"+url.PathEscape(r.RemoteID)+"/resolve", body)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), strings.TrimSuffix(string(out), "\n"))
			return err
		},
	}
	resolve.Flags().StringVar(&def, "default", "", "choice for hunks not named (default library)")
	return resolve
}

func applyCmd() *cobra.Command {
	var verdicts []string
	apply := &cobra.Command{
		Use:   "apply <sync-job-id> [remote-id ...]",
		Short: "Resolve a sync's items as it suggested",
		Long: `Apply starts one job that resolves each item the sync has a suggestion
for, or only the remote IDs named. A bind keeps the library's side of every hunk.`,
		Example: "  ink sync apply 41 --verdict exact\n  ink sync apply 41 brakka.png",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			jobID, err := parseID(args[0])
			if err != nil {
				return err
			}
			remoteIDs := args[1:]
			c, err := newClient(cmd)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			snap, err := snapshot(ctx, c, jobID)
			if err != nil {
				return err
			}
			inst := fmt.Sprint(snap.Params.InstanceID)
			var rs []wire.Resolution
			add := func(kind, itemsPath string, it wire.ReconcileItem) error {
				s := it.Suggested
				if s == nil || len(verdicts) > 0 && !slices.Contains(verdicts, it.Verdict) ||
					len(remoteIDs) > 0 && !slices.Contains(remoteIDs, it.RemoteID) {
					return nil
				}
				r := wire.Resolution{InstanceID: snap.Params.InstanceID, RemoteID: it.RemoteID, Kind: kind,
					AppHash: it.AppHash, BindingID: it.BindingID, Action: s.Action, CardID: s.CardID}
				if s.Action == "bind" {
					// The snapshot doesn't carry the card's head, so a bind reads it
					// from a comparison; the snapshot's app_hash still guards the app.
					path := "/instances/" + inst + "/" + itemsPath + "/" + url.PathEscape(it.RemoteID) + "/compare"
					q := "card_id"
					if kind == "lorebook" {
						q = "lorebook_id"
					}
					cmp, err := fetchComparison(ctx, c, fmt.Sprintf("%s?%s=%d", path, q, s.CardID))
					if err != nil {
						return fmt.Errorf("%s: %w", it.RemoteID, err)
					}
					r.BaseRevisionID, r.AcceptedHash = cmp.HeadRevisionID, cmp.AcceptedHash
				}
				rs = append(rs, r)
				return nil
			}
			for _, it := range snap.Items {
				if err := add("card", "items", it); err != nil {
					return err
				}
			}
			for _, lb := range snap.Lorebooks {
				if err := add("lorebook", "lorebook-items", lb); err != nil {
					return err
				}
			}
			if len(rs) == 0 {
				return errors.New("no suggestions match")
			}
			return startJob(cmd, "/resolutions", map[string]any{"resolutions": rs})
		},
	}
	apply.Flags().StringSliceVar(&verdicts, "verdict", nil, "only items with these verdicts: exact, likely, new")
	return apply
}

// snapshot reads a finished reconcile job's snapshot.
func snapshot(ctx context.Context, c *client, jobID string) (wire.Snapshot, error) {
	var snap wire.Snapshot
	out, err := c.do(ctx, "GET", "/jobs/"+jobID, nil)
	if err != nil {
		return snap, err
	}
	var j struct {
		Kind     string          `json:"kind"`
		Status   string          `json:"status"`
		Progress json.RawMessage `json:"progress"`
	}
	if err := json.Unmarshal(out, &j); err != nil {
		return snap, err
	}
	if j.Kind != wire.ReconcileKind || j.Status != "succeeded" {
		return snap, fmt.Errorf("job %s is a %s job that is %s, not a finished sync", jobID, j.Kind, j.Status)
	}
	return snap, json.Unmarshal(j.Progress, &snap)
}

// printSnapshot prints one row per app item, then the totals.
func printSnapshot(w io.Writer, snap wire.Snapshot) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "KIND\tVERDICT\tREMOTE ID\tNAME\tTOP CANDIDATE\tSUGGESTED")
	// Nested world books get rows, so they count toward both numbers.
	var applied, pending int
	row := func(kind string, it wire.ReconcileItem) {
		top := ""
		if len(it.Candidates) > 0 {
			top = fmt.Sprintf("%s (%d)", it.Candidates[0].Name, it.Candidates[0].CardID)
		}
		suggested := ""
		if s := it.Suggested; s != nil {
			suggested = s.Action
			if s.CardID != 0 {
				suggested += fmt.Sprintf(" %d", s.CardID)
			}
		}
		if it.Error != "" {
			suggested = "error: " + it.Error
		}
		verdict := it.Verdict
		if it.AutoBound || it.AutoImported {
			applied++
			verdict += " (auto)"
		} else if it.Verdict != "bound" {
			pending++
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", kind, verdict, it.RemoteID, it.Name, top, suggested)
	}
	for _, it := range snap.Items {
		row("card", it)
		for _, lb := range it.Lorebooks {
			row("  lorebook", lb)
		}
	}
	for _, lb := range snap.Lorebooks {
		row("lorebook", lb)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	verdicts := make([]string, 0, len(snap.Totals))
	for v, n := range snap.Totals {
		verdicts = append(verdicts, fmt.Sprintf("%s %d", v, n))
	}
	slices.Sort(verdicts)
	fmt.Fprintln(w, "totals:", strings.Join(verdicts, ", "))
	fmt.Fprintf(w, "%d applied, %d left to review\n", applied, pending)
	if len(snap.Missing) > 0 {
		fmt.Fprintln(w, "bound cards the app no longer has:", snap.Missing)
	}
	if len(snap.Unpushed) > 0 {
		fmt.Fprintln(w, "cards never pushed here:", len(snap.Unpushed))
	}
	return nil
}

// comparePath is an app character's compare route, with card_id when given.
func comparePath(inst, remoteID, card string) string {
	p := "/instances/" + inst + "/items/" + url.PathEscape(remoteID) + "/compare"
	if card != "" {
		p += "?card_id=" + card
	}
	return p
}

func fetchComparison(ctx context.Context, c *client, path string) (wire.Comparison, error) {
	var cmp wire.Comparison
	out, err := c.do(ctx, "GET", path, nil)
	if err != nil {
		return cmp, err
	}
	return cmp, json.Unmarshal(out, &cmp)
}

// printComparison prints each hunk that isn't the same on both sides, with
// changed words marked [-library-] and {+app+}.
func printComparison(w io.Writer, cmp wire.Comparison) {
	fmt.Fprintf(w, "%s against card %d (revision %d, base %s)\n", cmp.RemoteID, cmp.CardID, cmp.HeadRevisionID, cmp.Base)
	same := true
	for _, f := range cmp.Fields {
		for _, h := range f.Hunks {
			if h.Status == merge.Same {
				continue
			}
			same = false
			fmt.Fprintf(w, "\n%s  %s\n", h.ID, h.Status)
			fmt.Fprintf(w, "  library: %s\n", marked(h.Library, h.Words, "library", "[-", "-]"))
			fmt.Fprintf(w, "  app:     %s\n", marked(h.App, h.Words, "app", "{+", "+}"))
		}
	}
	if a := cmp.Avatar; a != nil {
		same = false
		fmt.Fprintf(w, "\navatar  differs (library %s, app %s)\n", or(a.Library, "none"), or(a.App, "none"))
	}
	if same {
		fmt.Fprintln(w, "\nno differences")
	}
}

func or(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// marked returns a hunk side as text: a JSON string decoded with its
// side's spans wrapped in open and close, anything else as JSON.
func marked(raw json.RawMessage, spans []merge.Span, side, open, close string) string {
	if len(raw) == 0 {
		return "(absent)"
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return string(raw)
	}
	var b strings.Builder
	at := 0
	for _, sp := range spans {
		if sp.Side != side || sp.Start < at || sp.End > len(s) {
			continue
		}
		b.WriteString(s[at:sp.Start] + open + s[sp.Start:sp.End] + close)
		at = sp.End
	}
	b.WriteString(s[at:])
	return b.String()
}
