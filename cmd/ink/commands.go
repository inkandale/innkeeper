package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

// jsonArg returns args[i] if it is valid JSON, or def if args has no index i.
func jsonArg(args []string, i int, def string) (json.RawMessage, error) {
	if len(args) <= i {
		return json.RawMessage(def), nil
	}
	if !json.Valid([]byte(args[i])) {
		return nil, fmt.Errorf("%q is not valid JSON", args[i])
	}
	return json.RawMessage(args[i]), nil
}

func jsonFlag(cmd *cobra.Command) bool {
	v, _ := cmd.Flags().GetBool("json")
	return v
}

// show prints an API response as it came with --json, and otherwise decodes
// it and prints it with table.
func show[T any](cmd *cobra.Command, raw []byte, table func(io.Writer, T) error) error {
	w := cmd.OutOrStdout()
	if jsonFlag(cmd) {
		_, err := fmt.Fprintln(w, strings.TrimSuffix(string(raw), "\n"))
		return err
	}
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	return table(w, v)
}

func callCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "call <method> <path> [json]",
		Short:   "Make any API call and print the response",
		Long:    "Call reaches API routes that have no command of their own. The path is under /api; the API docs are at /api/docs on the server.",
		Example: "  ink call GET /jobs\n  ink call PATCH /settings '{\"theme\": \"dusk\"}'",
		Args:    cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := jsonArg(args, 2, "")
			if err != nil {
				return err
			}
			if len(body) == 0 {
				body = nil
			}
			c, err := newClient()
			if err != nil {
				return err
			}
			out, err := c.do(cmd.Context(), strings.ToUpper(args[0]), args[1], body)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), strings.TrimSuffix(string(out), "\n"))
			return err
		},
	}
}

// startJob posts body to path, which starts a job, and says how to follow it.
func startJob(cmd *cobra.Command, path string, body any) error {
	c, err := newClient()
	if err != nil {
		return err
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	out, err := c.do(cmd.Context(), "POST", path, raw)
	if err != nil {
		return err
	}
	return show(cmd, out, func(w io.Writer, j job) error {
		_, err := fmt.Fprintf(w, "started job %d; follow it with: ink jobs watch %d\n", j.ID, j.ID)
		return err
	})
}

func importCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "import <instance-id> [params-json]",
		Short: "Start an import from an importer or app instance",
		Example: `  ink import 4                                  # everything the instance offers
  ink import 4 '{"path": "fantasy"}'            # a folder importer's subfolder
  ink import 5 '{"remote_ids": ["brakka.png"]}' # only these app items`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			params, err := jsonArg(args, 1, `{}`)
			if err != nil {
				return err
			}
			return startJob(cmd, "/imports", map[string]any{"instance_id": json.Number(id), "params": params})
		},
	}
}

// job is a row of GET /jobs.
type job struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	Error     *string   `json:"error"`
}

func printJobs(w io.Writer, list []job) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tKIND\tSTATUS\tCREATED\tERROR")
	for _, j := range list {
		e := ""
		if j.Error != nil {
			e = *j.Error
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n", j.ID, j.Kind, j.Status, j.CreatedAt.Local().Format(time.DateTime), e)
	}
	return tw.Flush()
}

func printJob(w io.Writer, j job) error { return printJobs(w, []job{j}) }

func jobsCmd() *cobra.Command {
	jobs := &cobra.Command{
		Use:   "jobs",
		Short: "List jobs, or show, cancel, or watch one",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newClient()
			if err != nil {
				return err
			}
			out, err := c.do(cmd.Context(), "GET", "/jobs", nil)
			if err != nil {
				return err
			}
			return show(cmd, out, printJobs)
		},
	}
	one := func(use, short, method, suffix string) *cobra.Command {
		return &cobra.Command{
			Use:   use + " <job-id>",
			Short: short,
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				id, err := parseID(args[0])
				if err != nil {
					return err
				}
				c, err := newClient()
				if err != nil {
					return err
				}
				out, err := c.do(cmd.Context(), method, "/jobs/"+id+suffix, nil)
				if err != nil {
					return err
				}
				return show(cmd, out, printJob)
			},
		}
	}
	jobs.AddCommand(
		one("show", "Show a job; --json includes its progress and result", "GET", ""),
		one("cancel", "Cancel a job", "POST", "/cancel"),
		&cobra.Command{
			Use:   "watch <job-id>",
			Short: "Print a line each time a job changes, and fail unless it succeeds",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				id, err := parseID(args[0])
				if err != nil {
					return err
				}
				c, err := newClient()
				if err != nil {
					return err
				}
				return watchJob(cmd.Context(), c, id, cmd.OutOrStdout())
			},
		},
	)
	return jobs
}

// watchJob prints a line each time the job changes, and fails unless the job
// succeeds.
func watchJob(ctx context.Context, c *client, id string, w io.Writer) error {
	status := ""
	err := c.stream(ctx, "/jobs/"+id+"/events", func(data []byte) error {
		var j struct {
			Status   string          `json:"status"`
			Progress json.RawMessage `json:"progress"`
			Children map[string]int  `json:"children"`
			Outcomes map[string]int  `json:"outcomes"`
			Error    *string         `json:"error"`
		}
		if err := json.Unmarshal(data, &j); err != nil {
			return err
		}
		status = j.Status
		line := j.Status + " " + string(j.Progress)
		if len(j.Children) > 0 {
			line += fmt.Sprintf(" children %v", j.Children)
		}
		if len(j.Outcomes) > 0 {
			line += fmt.Sprintf(" outcomes %v", j.Outcomes)
		}
		if j.Error != nil {
			line += " error: " + *j.Error
		}
		fmt.Fprintln(w, line)
		return nil
	})
	if err != nil {
		return err
	}
	switch status {
	case "succeeded":
		return nil
	case "failed":
		return errors.New("job failed")
	case "":
		status = "unknown"
	}
	return fmt.Errorf("job stream ended while the job was %s; check it with: ink jobs show %s", status, id)
}
