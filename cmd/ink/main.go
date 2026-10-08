// Command ink is the Innkeeper client.
package main

import (
	"fmt"
	"os"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// version is set at release with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := newRoot().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "ink:", err)
		os.Exit(1)
	}
}

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "ink",
		Short: "Work with an Innkeeper server",
		Long: `ink calls the API of the Innkeeper server you logged in to with: ink login <url>
For scripts, set INNKEEPER_URL and an API token in INNKEEPER_TOKEN; they take precedence.`,
		Version:       buildVersion(),
		SilenceErrors: true,
		// Usage helps with a wrong argument or flag, which cobra reports
		// before this runs; it only buries an API error.
		PersistentPreRun: func(cmd *cobra.Command, _ []string) { cmd.SilenceUsage = true },
	}
	root.PersistentFlags().Bool("json", false, "print JSON instead of tables")
	root.PersistentFlags().String("profile", "", "the login to use, instead of the default")
	_ = root.RegisterFlagCompletionFunc("profile", completeProfiles)
	root.AddCommand(callCmd(), importCmd(), jobsCmd(), cardsCmd(), syncCmd(), profilesCmd(), loginCmd())
	return root
}

// buildVersion is the release version, or the module version for go install.
func buildVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok && version == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return version
}
