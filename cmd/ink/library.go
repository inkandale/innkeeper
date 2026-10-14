package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

// kind names a library type, cards or lorebooks, in URLs and messages.
type kind struct{ plural, singular string }

var (
	cardKind     = kind{"cards", "card"}
	lorebookKind = kind{"lorebooks", "lorebook"}
)

func (k kind) searchPath() string { return "/" + k.plural + "/search" }

// entityCmds returns the commands cards and lorebooks share.
func entityCmds(k kind) []*cobra.Command {
	return []*cobra.Command{
		listCmd(k),
		searchCmd(k),
		getCmd(k, "show", "Show a "+k.singular+"; --json includes its data", "", printEntity),
		getCmd(k, "history", "List a "+k.singular+"'s revisions, newest first", "/revisions", printRevisions),
		createCmd(k),
		editCmd(k),
		archiveCmd(k, "archive"),
		archiveCmd(k, "unarchive"),
		importFileCmd(k),
		importURLCmd(k),
	}
}

func importFileCmd(k kind) *cobra.Command {
	return &cobra.Command{
		Use:   "import <instance-id> <file>",
		Short: "Import a card or lorebook file with an importer instance",
		Long: `Import uploads the file, then starts an import of it with the instance, such
as one of the card-import plugin. The instance decides whether the file holds
a card or a lorebook.`,
		Example: "  ink " + k.plural + " import 4 brakka.png",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			file, err := os.ReadFile(args[1])
			if err != nil {
				return err
			}
			c, err := newClient(cmd)
			if err != nil {
				return err
			}
			sha, err := c.upload(cmd.Context(), file)
			if err != nil {
				return err
			}
			return startJob(cmd, "/imports", map[string]any{"instance_id": json.Number(id),
				"params": map[string]string{"blob": sha, "name": filepath.Base(args[1])}})
		},
	}
}

func importURLCmd(k kind) *cobra.Command {
	return &cobra.Command{
		Use:     "import-url <instance-id> <url>",
		Short:   "Import a card or lorebook from its page URL with an importer instance, such as one of url-import",
		Example: "  ink " + k.plural + " import-url 7 https://chub.ai/characters/user/name",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			return startJob(cmd, "/imports", map[string]any{"instance_id": json.Number(id),
				"params": map[string]string{"url": args[1]}})
		},
	}
}

// archiveCmd archives or unarchives one entity by ID, or what a query
// matches with a bulk job.
func archiveCmd(k kind, action string) *cobra.Command {
	return &cobra.Command{
		Use:   action + " <" + k.singular + "-id>|<query-json>",
		Short: strings.ToUpper(action[:1]) + action[1:] + " a " + k.singular + ", or the " + k.plural + " a search matches",
		Long: "A query starts a job. It needs a filter or text; to select every " + k.singular +
			", filter on archived. " + strings.ToUpper(k.plural[:1]) + k.plural[1:] + " already in that state are left alone.",
		Example: "  ink " + k.plural + " " + action + " 12\n  ink " + k.plural + " " + action + ` '{"filter": {"source": "chub"}}'`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if id, err := parseID(args[0]); err == nil {
				c, err := newClient(cmd)
				if err != nil {
					return err
				}
				body := []byte(fmt.Sprintf(`{"archived":%t}`, action == "archive"))
				_, err = c.do(cmd.Context(), "PUT", "/"+k.plural+"/"+id+"/archived", body)
				return err
			}
			if !strings.HasPrefix(strings.TrimSpace(args[0]), "{") {
				return fmt.Errorf("%q is neither an ID nor a query", args[0])
			}
			query, err := jsonArg(args, 0, "")
			if err != nil {
				return err
			}
			body := map[string]any{"query": query, "action": action}
			if k != cardKind {
				body["kind"] = k.singular
			}
			return startJob(cmd, "/bulk", body)
		},
	}
}

// group makes a command that only holds subcommands. Unlike cobra's default,
// it fails on an unknown subcommand.
func group(use, short string) *cobra.Command {
	return &cobra.Command{Use: use, Short: short, Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() }}
}

func lorebooksCmd() *cobra.Command {
	lorebooks := group("lorebooks", "Work with library lorebooks")
	lorebooks.AddCommand(entityCmds(lorebookKind)...)
	lorebooks.AddCommand(lorebookExportCmd(), sourcesCmd(lorebookKind))
	return lorebooks
}

func lorebookExportCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "export <lorebook-id> [file]",
		Short:   "Export a lorebook as CCv3 JSON to a file, or to stdout",
		Example: "  ink lorebooks export 5 orcs.json",
		Args:    cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			c, err := newClient(cmd)
			if err != nil {
				return err
			}
			out, err := c.do(cmd.Context(), "GET", "/lorebooks/"+id, nil)
			if err != nil {
				return err
			}
			var e entity
			if err := json.Unmarshal(out, &e); err != nil {
				return err
			}
			// The API has no lorebook export; CCv3's standalone lorebook is this wrapper.
			file, err := json.Marshal(struct {
				Spec string          `json:"spec"`
				Data json.RawMessage `json:"data"`
			}{"lorebook_v3", e.Revision.Data})
			if err != nil {
				return err
			}
			if len(args) == 1 {
				_, err = fmt.Fprintln(cmd.OutOrStdout(), string(file))
				return err
			}
			return os.WriteFile(args[1], file, 0o644)
		},
	}
}

// hit is a row of a search.
type hit struct {
	ID         int64          `json:"id"`
	Slug       string         `json:"slug"`
	ArchivedAt *time.Time     `json:"archived_at"`
	Fields     map[string]any `json:"fields"`
	Tags       []string       `json:"tags"`
	Entries    *int           `json:"entries"`
}

type results struct {
	Hits  []hit `json:"hits"`
	Total int   `json:"total"`
}

// pageFlags adds --limit and --page, which runSearch reads.
func pageFlags(cmd *cobra.Command) {
	cmd.Flags().Int("limit", 0, "rows per page, up to 500 (default 50)")
	cmd.Flags().Int("page", 0, "the page to print (default 1)")
}

// runSearch sends query, with the page the flags pick, and prints the hits.
// When more match, it names the next page on stderr.
func runSearch(cmd *cobra.Command, k kind, query map[string]any) error {
	limit, _ := cmd.Flags().GetInt("limit")
	page, _ := cmd.Flags().GetInt("page")
	// The flags override the query's own page, field by field.
	p, _ := query["page"].(map[string]any)
	if p == nil {
		p = map[string]any{}
	}
	if limit > 0 {
		p["size"] = limit
	}
	if page > 0 {
		p["number"] = page
	}
	if len(p) > 0 {
		query["page"] = p
	}
	size, _ := p["size"].(float64)
	number, _ := p["number"].(float64)
	limit, page = cmp.Or(limit, int(size)), cmp.Or(page, int(number))
	if _, ok := query["include"]; !ok && k == cardKind {
		query["include"] = []string{"tags"}
	}
	body, err := json.Marshal(query)
	if err != nil {
		return err
	}
	c, err := newClient(cmd)
	if err != nil {
		return err
	}
	out, err := c.do(cmd.Context(), "POST", k.searchPath(), body)
	if err != nil {
		return err
	}
	return show(cmd, out, func(w io.Writer, r results) error {
		extra := "TAGS"
		if k == lorebookKind {
			extra = "ENTRIES"
		}
		tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		fmt.Fprintf(tw, "ID\tSLUG\tNAME\t%s\tARCHIVED\n", extra)
		for _, h := range r.Hits {
			name, _ := h.Fields["name"].(string)
			x := strings.Join(h.Tags, ",")
			if k == lorebookKind && h.Entries != nil {
				x = fmt.Sprint(*h.Entries)
			}
			fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n", h.ID, h.Slug, name, x, date(h.ArchivedAt))
		}
		if err := tw.Flush(); err != nil {
			return err
		}
		if shown := len(r.Hits) + max(page-1, 0)*cmp.Or(limit, 50); shown < r.Total {
			fmt.Fprintf(cmd.ErrOrStderr(), "%d of %d; next: --page %d\n", shown, r.Total, max(page, 1)+1)
		}
		return nil
	})
}

// date is t's local date, or nothing for nil.
func date(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Local().Format(time.DateOnly)
}

func listCmd(k kind) *cobra.Command {
	var archived bool
	list := &cobra.Command{
		Use:   "list",
		Short: "List " + k.plural + ", newest first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			q := map[string]any{}
			if archived {
				q["filter"] = map[string]bool{"archived": true}
			}
			return runSearch(cmd, k, q)
		},
	}
	list.Flags().BoolVar(&archived, "archived", false, "list archived "+k.plural+" instead")
	pageFlags(list)
	return list
}

func searchCmd(k kind) *cobra.Command {
	search := &cobra.Command{
		Use:   "search <text>|<query-json>",
		Short: "Search " + k.plural + " by text, or with a query",
		Long: `Text supports "quoted phrases", or, and -excluded words. An argument that
starts with { is a whole query, as POST /api` + k.searchPath() + ` takes; see /api/docs.`,
		Example: "  ink " + k.plural + ` search 'orc -goblin'
  ink ` + k.plural + ` search '{"filter": {"source": "chub"}, "sort": "name"}'`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			q := map[string]any{"text": args[0]}
			if strings.HasPrefix(strings.TrimSpace(args[0]), "{") {
				q = map[string]any{}
				if err := json.Unmarshal([]byte(args[0]), &q); err != nil {
					return fmt.Errorf("%q is not a valid query: %w", args[0], err)
				}
			}
			return runSearch(cmd, k, q)
		},
	}
	pageFlags(search)
	return search
}

// entity is GET /cards/{id} or /lorebooks/{id}.
type entity struct {
	ID          int64      `json:"id"`
	Slug        string     `json:"slug"`
	Provenance  string     `json:"provenance"`
	ArchivedAt  *time.Time `json:"archived_at"`
	CreatedAt   time.Time  `json:"created_at"`
	Revision    revision   `json:"revision"`
	Tags        []string   `json:"tags"`
	Lorebooks   []int64    `json:"lorebooks"`
	Collections []int64    `json:"collections"`
	Cards       []int64    `json:"cards"`
}

// revision is a row of a history, or an entity's head with its data.
type revision struct {
	ID        int64           `json:"id"`
	Data      json.RawMessage `json:"data"`
	Message   string          `json:"message"`
	Source    string          `json:"source"`
	CreatedAt time.Time       `json:"created_at"`
}

func printEntity(w io.Writer, e entity) error {
	var d struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(e.Revision.Data, &d)
	ids := func(v []int64) string { return strings.Trim(fmt.Sprint(v), "[]") }
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	for _, row := range [][2]string{
		{"ID", fmt.Sprint(e.ID)},
		{"SLUG", e.Slug},
		{"NAME", d.Name},
		{"PROVENANCE", e.Provenance},
		{"CREATED", e.CreatedAt.Local().Format(time.DateTime)},
		{"REVISION", fmt.Sprintf("%d, %s", e.Revision.ID, e.Revision.CreatedAt.Local().Format(time.DateTime))},
		{"ARCHIVED", date(e.ArchivedAt)},
		{"TAGS", strings.Join(e.Tags, ", ")},
		{"LOREBOOKS", ids(e.Lorebooks)},
		{"COLLECTIONS", ids(e.Collections)},
		{"CARDS", ids(e.Cards)},
	} {
		if row[1] != "" {
			fmt.Fprintf(tw, "%s\t%s\n", row[0], row[1])
		}
	}
	return tw.Flush()
}

func printRevisions(w io.Writer, list []revision) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tCREATED\tSOURCE\tMESSAGE")
	for _, r := range list {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\n", r.ID, r.CreatedAt.Local().Format(time.DateTime), r.Source, r.Message)
	}
	return tw.Flush()
}

// getCmd makes a command that gets path under an entity and prints it.
func getCmd[T any](k kind, use, short, suffix string, table func(io.Writer, T) error) *cobra.Command {
	return &cobra.Command{
		Use:   use + " <" + k.singular + "-id>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			c, err := newClient(cmd)
			if err != nil {
				return err
			}
			out, err := c.do(cmd.Context(), "GET", "/"+k.plural+"/"+id+suffix, nil)
			if err != nil {
				return err
			}
			return show(cmd, out, table)
		},
	}
}
