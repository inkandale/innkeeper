package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func cardsCmd() *cobra.Command {
	cards := group("cards", "Work with library cards")
	cards.AddCommand(entityCmds(cardKind)...)
	cards.AddCommand(exportCmd(), sourcesCmd())
	for _, action := range []string{"archive", "unarchive"} {
		cards.AddCommand(&cobra.Command{
			Use:     action + " <query-json>",
			Short:   strings.ToUpper(action[:1]) + action[1:] + " the cards a search matches",
			Long:    "The query needs a filter or text; to select every card, filter on archived. Cards already in that state are left alone.",
			Example: "  ink cards " + action + ` '{"filter": {"tag": "fantasy"}}'`,
			Args:    cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				query, err := jsonArg(args, 0, "")
				if err != nil {
					return err
				}
				return startJob(cmd, "/bulk", map[string]any{"query": query, "action": action})
			},
		})
	}
	return cards
}

func exportCmd() *cobra.Command {
	var format string
	export := &cobra.Command{
		Use:   "export <card-id> [file]",
		Short: "Export a card to a file, or to stdout",
		Long: `Export writes CCv3 JSON by default, or PNG for a .png file. Any other format,
PNG included, needs an instance of a codec plugin that writes it.`,
		Example: "  ink cards export 12 brakka.png\n  ink cards export 12 --format charx > brakka.charx",
		Args:    cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			f := format
			if f == "" {
				f = "json"
				if len(args) == 2 && strings.EqualFold(filepath.Ext(args[1]), ".png") {
					f = "png"
				}
			}
			c, err := newClient(cmd)
			if err != nil {
				return err
			}
			out, err := c.do(cmd.Context(), "GET", "/cards/"+id+"/export?format="+url.QueryEscape(f), nil)
			if err != nil {
				return err
			}
			if len(args) == 1 {
				_, err = cmd.OutOrStdout().Write(out)
				return err
			}
			return os.WriteFile(args[1], out, 0o644)
		},
	}
	export.Flags().StringVar(&format, "format", "", "json, png, or a codec's format (default json, or png for a .png file)")
	return export
}

// source is a row of GET /cards/{id}/sources.
type source struct {
	ID       int64  `json:"id"`
	Kind     string `json:"kind"`
	Ref      string `json:"ref"`
	LinkedBy string `json:"linked_by"`
	URL      string `json:"url"`
}

func printSources(w io.Writer, list []source) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tKIND\tREF\tLINKED BY\tURL")
	for _, s := range list {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n", s.ID, s.Kind, s.Ref, s.LinkedBy, s.URL)
	}
	return tw.Flush()
}

func sourcesCmd() *cobra.Command {
	sources := &cobra.Command{
		Use:   "sources <card-id>",
		Short: "List, add, or remove the sites a card was published on",
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
			out, err := c.do(cmd.Context(), "GET", "/cards/"+id+"/sources", nil)
			if err != nil {
				return err
			}
			return show(cmd, out, printSources)
		},
	}
	sources.AddCommand(&cobra.Command{
		Use:     "add <card-id> <url>",
		Short:   "Add a source to a card",
		Example: "  ink cards sources add 12 https://chub.ai/characters/user/name",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			body, err := json.Marshal(map[string]string{"url": args[1]})
			if err != nil {
				return err
			}
			c, err := newClient(cmd)
			if err != nil {
				return err
			}
			_, err = c.do(cmd.Context(), "PUT", "/cards/"+id+"/sources", body)
			return err
		},
	}, &cobra.Command{
		Use:   "remove <card-id> <source-id>",
		Short: "Remove a source from a card; detection won't add it back",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			sid, err := parseID(args[1])
			if err != nil {
				return err
			}
			c, err := newClient(cmd)
			if err != nil {
				return err
			}
			_, err = c.do(cmd.Context(), "DELETE", "/cards/"+id+"/sources/"+sid, nil)
			return err
		},
	})
	return sources
}
