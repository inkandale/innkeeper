package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// plugin is a row of GET /plugins.
type plugin struct {
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Capabilities []string `json:"capabilities"`
	Source       string   `json:"source"`
}

// pluginSource is a row of GET /plugin-sources.
type pluginSource struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

// catalogSource is a row of GET /plugin-catalog.
type catalogSource struct {
	Source  pluginSource `json:"source"`
	Plugins []struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Versions    []string `json:"versions"` // newest first
	} `json:"plugins"`
	Error string `json:"error"`
}

func printPlugins(w io.Writer, list []plugin) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tVERSION\tSOURCE\tCAPABILITIES")
	for _, p := range list {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", p.Name, p.Version, p.Source, strings.Join(p.Capabilities, ","))
	}
	return tw.Flush()
}

func printCatalog(w io.Writer, list []catalogSource) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "SOURCE\tPLUGIN\tVERSIONS\tDESCRIPTION")
	for _, s := range list {
		if s.Error != "" {
			fmt.Fprintf(tw, "%s\t\t\terror: %s\n", s.Source.ID, s.Error)
		}
		for _, p := range s.Plugins {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", s.Source.ID, p.Name, strings.Join(p.Versions, ", "), p.Description)
		}
	}
	return tw.Flush()
}

func printPluginSources(w io.Writer, list []pluginSource) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tURL")
	for _, s := range list {
		fmt.Fprintf(tw, "%s\t%s\n", s.ID, s.URL)
	}
	return tw.Flush()
}

// pickInstall fills in the source and version the catalog offers name at:
// the newest version, from the only source that has it.
func pickInstall(catalog []catalogSource, name, source, version string) (string, string, error) {
	type offer struct{ source, version string }
	var offers []offer
	for _, s := range catalog {
		if source != "" && s.Source.ID != source {
			continue
		}
		for _, p := range s.Plugins {
			if p.Name != name || len(p.Versions) == 0 {
				continue
			}
			if version == "" {
				offers = append(offers, offer{s.Source.ID, p.Versions[0]})
			} else if slices.Contains(p.Versions, version) {
				offers = append(offers, offer{s.Source.ID, version})
			}
		}
	}
	switch len(offers) {
	case 0:
		return "", "", fmt.Errorf("no source offers %s %s; see: ink plugins catalog", name, version)
	case 1:
		return offers[0].source, offers[0].version, nil
	}
	ids := make([]string, len(offers))
	for i, o := range offers {
		ids[i] = o.source
	}
	return "", "", fmt.Errorf("sources %s all offer %s; pick one with --source", strings.Join(ids, ", "), name)
}

func pluginsCmd() *cobra.Command {
	plugins := &cobra.Command{
		Use:   "plugins",
		Short: "List installed plugins, or install, remove, or purge one",
		Long:  "Plugins run as code inside Innkeeper, so changing them needs a destructive token or login.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out, err := callAPI(cmd, "GET", "/plugins", nil)
			if err != nil {
				return err
			}
			return show(cmd, out, printPlugins)
		},
	}
	// byName runs method on /plugins/<name><suffix>.
	byName := func(method, suffix string) func(*cobra.Command, []string) error {
		return func(cmd *cobra.Command, args []string) error {
			_, err := callAPI(cmd, method, "/plugins/"+url.PathEscape(args[0])+suffix, nil)
			return err
		}
	}
	var yes bool
	purge := &cobra.Command{
		Use:   "purge <name>",
		Short: "Delete an uninstalled plugin's instances, database schema, and role",
		Long:  "Purge can't be undone. It asks first, unless --yes is given.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := confirm(cmd, yes, "Delete every instance and all data of "+args[0]+"? This can't be undone."); err != nil {
				return err
			}
			return byName("DELETE", "/data")(cmd, args)
		},
	}
	purge.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask first")
	plugins.AddCommand(&cobra.Command{
		Use:   "catalog",
		Short: "List the plugins each source offers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out, err := callAPI(cmd, "GET", "/plugin-catalog", nil)
			if err != nil {
				return err
			}
			return show(cmd, out, printCatalog)
		},
	}, installPluginCmd(), &cobra.Command{
		Use:   "remove <name>",
		Short: "Uninstall a plugin; its instances and data stay for a reinstall",
		Args:  cobra.ExactArgs(1),
		RunE:  byName("DELETE", ""),
	}, purge, pluginSourcesCmd())
	return plugins
}

func installPluginCmd() *cobra.Command {
	var source string
	install := &cobra.Command{
		Use:   "install <name> [version]",
		Short: "Install or update a plugin from a source",
		Long: `Install takes the newest version unless given one, from the only source that
offers it unless --source names one. Installing the previous version rolls back
without a download. Jobs already running finish on the old version.`,
		Example: "  ink plugins install card-import\n  ink plugins install lumiverse 1.0.0 --source official",
		Args:    cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, version := args[0], ""
			if len(args) == 2 {
				version = args[1]
			}
			src := source
			if src == "" || version == "" {
				out, err := callAPI(cmd, "GET", "/plugin-catalog", nil)
				if err != nil {
					return err
				}
				var catalog []catalogSource
				if err := json.Unmarshal(out, &catalog); err != nil {
					return err
				}
				if src, version, err = pickInstall(catalog, name, source, version); err != nil {
					return err
				}
			}
			_, err := callAPI(cmd, "POST", "/plugins/"+url.PathEscape(name)+"/install", map[string]string{"source": src, "version": version})
			if err == nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "installed %s %s from %s\n", name, version, src)
			}
			return err
		},
	}
	install.Flags().StringVar(&source, "source", "", "the ID of the source to install from")
	return install
}

func pluginSourcesCmd() *cobra.Command {
	sources := &cobra.Command{
		Use:   "sources",
		Short: "List, add, or remove plugin sources",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out, err := callAPI(cmd, "GET", "/plugin-sources", nil)
			if err != nil {
				return err
			}
			return show(cmd, out, printPluginSources)
		},
	}
	sources.AddCommand(&cobra.Command{
		Use:     "add <id> <url>",
		Short:   "Add a source by the https:// or file:// URL of its index.toml",
		Long:    "A source's plugins run as code inside Innkeeper with their own database role, so add only sources you trust.",
		Example: "  ink plugins sources add mine https://example.com/index.toml",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := callAPI(cmd, "POST", "/plugin-sources", pluginSource{ID: args[0], URL: args[1]})
			return err
		},
	}, &cobra.Command{
		Use:   "remove <id>",
		Short: "Remove a source that no installed plugin came from",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := callAPI(cmd, "DELETE", "/plugin-sources/"+url.PathEscape(args[0]), nil)
			return err
		},
	})
	return sources
}
