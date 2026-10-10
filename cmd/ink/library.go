package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
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

func (k kind) searchPath() string {
	if k == cardKind {
		return "/search"
	}
	return "/" + k.plural + "/search"
}

// entityCmds returns the commands cards and lorebooks share.
func entityCmds(k kind) []*cobra.Command {
	return []*cobra.Command{listCmd(k), searchCmd(k)}
}

func lorebooksCmd() *cobra.Command {
	lorebooks := &cobra.Command{Use: "lorebooks", Short: "Work with library lorebooks"}
	lorebooks.AddCommand(entityCmds(lorebookKind)...)
	return lorebooks
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
	if limit > 0 || page > 0 {
		p := map[string]int{}
		if limit > 0 {
			p["size"] = limit
		}
		if page > 0 {
			p["number"] = page
		}
		query["page"] = p
	}
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
