package main

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// profile is a server ink logged in to. Its tokens are in the token store.
type profile struct {
	URL      string `json:"url"`
	ClientID string `json:"client_id"`
}

type config struct {
	Default  string             `json:"default,omitempty"`
	Profiles map[string]profile `json:"profiles"`
}

// configDir is where ink keeps its config and, without a keychain, its
// tokens. Tests replace it.
var configDir = func() (string, error) {
	d, err := os.UserConfigDir()
	return filepath.Join(d, "ink"), err
}

// A profile name becomes part of a file name.
var profileName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

func checkProfileName(name string) error {
	if !profileName.MatchString(name) {
		return fmt.Errorf("profile name %q must be 1 to 32 letters, digits, - or _", name)
	}
	return nil
}

func configFile() (string, error) {
	dir, err := configDir()
	return filepath.Join(dir, "config.json"), err
}

// loadConfig reads the config, or returns an empty one if there is none.
func loadConfig() (config, error) {
	c := config{Profiles: map[string]profile{}}
	path, err := configFile()
	if err != nil {
		return c, err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	if c.Profiles == nil {
		c.Profiles = map[string]profile{}
	}
	return c, nil
}

func (c config) save() error {
	path, err := configFile()
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return writePrivate(path, append(b, '\n'))
}

// profileFor returns the profile --profile names, or the default one.
func (c config) profileFor(cmd *cobra.Command) string {
	name, _ := cmd.Flags().GetString("profile")
	return cmp.Or(name, c.Default)
}

func completeProfiles(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	c, _ := loadConfig()
	return slices.Sorted(maps.Keys(c.Profiles)), cobra.ShellCompDirectiveNoFileComp
}

func profilesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profiles",
		Short: "List the servers ink is logged in to",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := loadConfig()
			if err != nil {
				return err
			}
			type row struct {
				Name    string `json:"name"`
				URL     string `json:"url"`
				Default bool   `json:"default"`
			}
			rows := []row{}
			for _, n := range slices.Sorted(maps.Keys(c.Profiles)) {
				rows = append(rows, row{n, c.Profiles[n].URL, n == c.Default})
			}
			w := cmd.OutOrStdout()
			if jsonFlag(cmd) {
				return json.NewEncoder(w).Encode(rows)
			}
			tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tURL\tDEFAULT")
			for _, r := range rows {
				def := ""
				if r.Default {
					def = "yes"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\n", r.Name, r.URL, def)
			}
			return tw.Flush()
		},
	}
	cmd.AddCommand(&cobra.Command{
		Use:               "use <name>",
		Short:             "Make a profile the default",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeProfiles,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadConfig()
			if err != nil {
				return err
			}
			if _, ok := c.Profiles[args[0]]; !ok {
				return fmt.Errorf("no profile %q", args[0])
			}
			c.Default = args[0]
			return c.save()
		},
	})
	return cmd
}
