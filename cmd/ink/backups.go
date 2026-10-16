package main

import (
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

// backup is a row of GET /backups.
type backup struct {
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	Size      int64     `json:"size"`
	Error     *string   `json:"error"`
}

func printBackups(w io.Writer, list []backup) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tTYPE\tSTATUS\tCREATED\tSIZE\tERROR")
	for _, b := range list {
		name, size, e := b.Name, "", ""
		if name == "" {
			name = "-"
		}
		if b.Size > 0 {
			size = strconv.FormatInt(b.Size, 10)
		}
		if b.Error != nil {
			e = *b.Error
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", name, b.Type, b.Status, b.CreatedAt.Local().Format(time.DateTime), size, e)
	}
	return tw.Flush()
}

func backupsCmd() *cobra.Command {
	backups := &cobra.Command{
		Use:   "backups",
		Short: "List backups, or create, download, or restore one",
		Long:  "Backups need a destructive token or login, since an assets backup holds every card and a config backup holds the encrypted secrets.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out, err := callAPI(cmd, "GET", "/backups", nil)
			if err != nil {
				return err
			}
			return show(cmd, out, printBackups)
		},
	}
	backups.AddCommand(&cobra.Command{
		Use:       "create assets|config",
		Short:     "Start a backup job; its progress names the backup when it's done",
		Args:      cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		ValidArgs: []string{"assets", "config"},
		RunE: func(cmd *cobra.Command, args []string) error {
			return startJob(cmd, "/backups", map[string]string{"type": args[0]})
		},
	}, &cobra.Command{
		Use:     "download <name> [file]",
		Short:   "Download a backup as a .tar.gz, to <name>.tar.gz by default",
		Example: "  ink backups download assets-20261010T030000Z assets.tar.gz",
		Args:    cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			file := filepath.Base(args[0]) + ".tar.gz"
			if len(args) == 2 {
				file = args[1]
			}
			return downloadBackup(cmd, args[0], file)
		},
	}, restoreCmd())
	return backups
}

// downloadBackup streams the backup name to file. A broken transfer leaves
// file as it was, so a truncated archive never looks whole.
func downloadBackup(cmd *cobra.Command, name, file string) (err error) {
	c, err := newClient(cmd)
	if err != nil {
		return err
	}
	resp, err := c.request(cmd.Context(), "GET", "/backups/"+url.PathEscape(name), "", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// Writing beside file and renaming keeps an existing copy until the new one is whole.
	f, err := os.CreateTemp(filepath.Dir(file), ".ink-download-*")
	if err != nil {
		return err
	}
	_, err = io.Copy(f, resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), file)
	}
	if err != nil {
		os.Remove(f.Name())
		return fmt.Errorf("download %s: %w", name, err)
	}
	return nil
}

func restoreCmd() *cobra.Command {
	var replace, yes bool
	restore := &cobra.Command{
		Use:   "restore <file>",
		Short: "Upload a backup archive and start a job that restores it",
		Long: `Restore covers the archive's type and leaves the other alone. Without --replace,
the job fails if the server already has data of that type. Restore assets before
config. Restore asks first, unless --yes is given.`,
		Example: "  ink backups restore assets.tar.gz\n  ink backups restore config.tar.gz --replace --yes",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := os.Open(args[0])
			if err != nil {
				return err
			}
			defer f.Close()
			q := "Restore " + args[0] + "?"
			if replace {
				q = "Restore " + args[0] + ", replacing the server's data of its type? This can't be undone."
			}
			if err := confirm(cmd, yes, q); err != nil {
				return err
			}
			c, err := newClient(cmd)
			if err != nil {
				return err
			}
			// The archive streams through a pipe, so a large one isn't held in memory.
			pr, pw := io.Pipe()
			mw := multipart.NewWriter(pw)
			go func() {
				part, err := mw.CreateFormFile("archive", filepath.Base(args[0]))
				if err == nil {
					_, err = io.Copy(part, f)
				}
				if err == nil {
					err = mw.WriteField("replace", strconv.FormatBool(replace))
				}
				if err == nil {
					err = mw.Close()
				}
				pw.CloseWithError(err)
			}()
			req, err := http.NewRequestWithContext(cmd.Context(), "POST", c.base+"/restores", pr)
			if err != nil {
				pr.Close()
				return err
			}
			req.Header.Set("Content-Type", mw.FormDataContentType())
			resp, err := c.send(req, "/restores")
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			out, err := io.ReadAll(resp.Body)
			if err != nil {
				return err
			}
			return jobStarted(cmd, out)
		},
	}
	restore.Flags().BoolVar(&replace, "replace", false, "replace the server's data of the archive's type")
	restore.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask first")
	return restore
}
