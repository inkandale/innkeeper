package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/davidgibbons/innkeeper/internal/merge"
	"github.com/davidgibbons/innkeeper/internal/targets"
)

// reconcileCmd runs `innkeeper reconcile INSTANCE_ID [PARAMS_JSON] [--json]`.
func reconcileCmd(args []string) error {
	asJSON := slices.Contains(args, "--json")
	args = slices.DeleteFunc(args, func(a string) bool { return a == "--json" })
	if len(args) < 1 || len(args) > 2 {
		return errors.New(usage)
	}
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
	params["instance_id"] = json.Number(inst)
	c, err := newClient()
	if err != nil {
		return err
	}
	ctx := context.Background()
	body, err := json.Marshal(params)
	if err != nil {
		return err
	}
	out, err := c.do(ctx, "POST", "/reconciles", body)
	if err != nil {
		return err
	}
	var j struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(out, &j); err != nil {
		return err
	}
	id := fmt.Sprint(j.ID)
	if err := watchJob(ctx, c, id, os.Stderr); err != nil {
		return err
	}
	snap, err := snapshot(ctx, c, id)
	if err != nil {
		return err
	}
	if asJSON {
		return json.NewEncoder(os.Stdout).Encode(snap)
	}
	fmt.Fprintf(os.Stderr, "reconcile job %d; resolve its suggestions with: innkeeper resolve --suggested %d\n", j.ID, j.ID)
	return printSnapshot(os.Stdout, snap)
}

// snapshot reads a finished reconcile job's snapshot.
func snapshot(ctx context.Context, c *client, jobID string) (targets.Snapshot, error) {
	var snap targets.Snapshot
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
	if j.Kind != targets.ReconcileKind || j.Status != "succeeded" {
		return snap, fmt.Errorf("job %s is a %s job that is %s, not a finished reconcile", jobID, j.Kind, j.Status)
	}
	return snap, json.Unmarshal(j.Progress, &snap)
}

// printSnapshot prints one row per app item, then the totals.
func printSnapshot(w io.Writer, snap targets.Snapshot) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "KIND\tVERDICT\tREMOTE ID\tNAME\tTOP CANDIDATE\tSUGGESTED")
	row := func(kind string, it targets.ReconcileItem) {
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
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", kind, it.Verdict, it.RemoteID, it.Name, top, suggested)
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

// compareCmd runs `innkeeper compare INSTANCE_ID REMOTE_ID [CARD_ID]`.
func compareCmd(args []string) error {
	if len(args) < 2 || len(args) > 3 {
		return errors.New(usage)
	}
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
	c, err := newClient()
	if err != nil {
		return err
	}
	cmp, err := fetchComparison(context.Background(), c, comparePath(inst, args[1], card))
	if err != nil {
		return err
	}
	printComparison(os.Stdout, cmp)
	return nil
}

func fetchComparison(ctx context.Context, c *client, path string) (targets.Comparison, error) {
	var cmp targets.Comparison
	out, err := c.do(ctx, "GET", path, nil)
	if err != nil {
		return cmp, err
	}
	return cmp, json.Unmarshal(out, &cmp)
}

// printComparison prints each hunk that isn't the same on both sides, with
// changed words marked [-library-] and {+app+}.
func printComparison(w io.Writer, cmp targets.Comparison) {
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

// resolveCmd runs `innkeeper resolve INSTANCE_ID REMOTE_ID ACTION [CARD_ID]
// [HUNK=CHOICE …] [--default=CHOICE]` or `innkeeper resolve --suggested
// JOB_ID [REMOTE_ID …] [--verdict=V,…]`.
func resolveCmd(args []string) error {
	if len(args) > 0 && args[0] == "--suggested" {
		return resolveSuggested(args[1:])
	}
	r := targets.Resolution{Choices: map[string]merge.Choice{}}
	var pos []string
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "--default="):
			r.Default = merge.Choice(strings.TrimPrefix(a, "--default="))
		case strings.HasPrefix(a, "--"):
			return fmt.Errorf("unknown flag %s\n%s", a, usage)
		case strings.Contains(a, "="):
			hunk, choice, _ := strings.Cut(a, "=")
			r.Choices[hunk] = merge.Choice(choice)
		default:
			pos = append(pos, a)
		}
	}
	if len(pos) < 3 || len(pos) > 4 {
		return errors.New(usage)
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
	c, err := newClient()
	if err != nil {
		return err
	}
	ctx := context.Background()
	if r.Action != "skip" {
		cmp, err := fetchComparison(ctx, c, comparePath(inst, r.RemoteID, card))
		if err != nil {
			if r.Action == "import" && card == "" {
				return fmt.Errorf("%w; an unbound item has no comparison, so import it from its reconcile: innkeeper resolve --suggested <job-id> %s", err, r.RemoteID)
			}
			return err
		}
		r.CardID, r.AppHash, r.BaseRevisionID, r.AcceptedHash = cmp.CardID, cmp.AppHash, cmp.HeadRevisionID, cmp.AcceptedHash
	}
	body, err := json.Marshal(r)
	if err != nil {
		return err
	}
	out, err := c.do(ctx, "POST", "/instances/"+inst+"/items/"+url.PathEscape(r.RemoteID)+"/resolve", body)
	if err != nil {
		return err
	}
	fmt.Println(strings.TrimSuffix(string(out), "\n"))
	return nil
}

// resolveSuggested sends a reconcile's suggestions as one bulk resolve. A
// bind keeps the library's side of every hunk.
func resolveSuggested(args []string) error {
	var verdicts, remoteIDs []string
	for _, a := range args {
		if v, ok := strings.CutPrefix(a, "--verdict="); ok {
			verdicts = strings.Split(v, ",")
		} else {
			remoteIDs = append(remoteIDs, a)
		}
	}
	if len(remoteIDs) == 0 {
		return errors.New(usage)
	}
	jobID, err := parseID(remoteIDs[0])
	if err != nil {
		return err
	}
	remoteIDs = remoteIDs[1:]
	c, err := newClient()
	if err != nil {
		return err
	}
	ctx := context.Background()
	snap, err := snapshot(ctx, c, jobID)
	if err != nil {
		return err
	}
	inst := fmt.Sprint(snap.Params.InstanceID)
	var rs []targets.Resolution
	add := func(kind, itemsPath string, it targets.ReconcileItem) error {
		s := it.Suggested
		if s == nil || len(verdicts) > 0 && !slices.Contains(verdicts, it.Verdict) ||
			len(remoteIDs) > 0 && !slices.Contains(remoteIDs, it.RemoteID) {
			return nil
		}
		r := targets.Resolution{InstanceID: snap.Params.InstanceID, RemoteID: it.RemoteID, Kind: kind,
			AppHash: it.AppHash, Action: s.Action, CardID: s.CardID}
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
	return startJob("/resolutions", map[string]any{"resolutions": rs})
}
