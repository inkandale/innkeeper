package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// callAPI sends body as JSON, or nothing if it is nil, and returns the response.
func callAPI(cmd *cobra.Command, method, path string, body any) ([]byte, error) {
	var raw []byte
	if body != nil {
		var err error
		if raw, err = json.Marshal(body); err != nil {
			return nil, err
		}
	}
	c, err := newClient(cmd)
	if err != nil {
		return nil, err
	}
	return c.do(cmd.Context(), method, path, raw)
}

// instance is a row of GET /instances.
type instance struct {
	ID           int64           `json:"id"`
	Plugin       string          `json:"plugin"`
	Name         string          `json:"name"`
	Config       json.RawMessage `json:"config"`
	Secrets      []string        `json:"secrets"`
	OwnerSubject *string         `json:"owner_subject"`
	BrokenError  *string         `json:"broken_error"`
}

func (i instance) status() string {
	if i.BrokenError != nil {
		return "broken"
	}
	return "ok"
}

func printInstances(w io.Writer, list []instance) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tPLUGIN\tNAME\tSTATUS")
	for _, i := range list {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\n", i.ID, i.Plugin, i.Name, i.status())
	}
	return tw.Flush()
}

func printInstance(w io.Writer, i instance) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "id:\t%d\nplugin:\t%s\nname:\t%s\nstatus:\t%s\n", i.ID, i.Plugin, i.Name, i.status())
	var config bytes.Buffer
	if err := json.Compact(&config, i.Config); err == nil {
		fmt.Fprintf(tw, "config:\t%s\n", config.String())
	}
	fmt.Fprintf(tw, "secrets:\t%s\n", strings.Join(i.Secrets, ", "))
	if i.OwnerSubject != nil {
		fmt.Fprintf(tw, "owner subject:\t%s\n", *i.OwnerSubject)
	}
	if i.BrokenError != nil {
		fmt.Fprintf(tw, "error:\t%s\n", *i.BrokenError)
	}
	return tw.Flush()
}

// readSecrets reads a JSON object of secrets from a file, or from stdin for "-".
func readSecrets(cmd *cobra.Command, path string) (map[string]*string, error) {
	var raw []byte
	var err error
	if path == "-" {
		raw, err = io.ReadAll(cmd.InOrStdin())
	} else {
		raw, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}
	var secrets map[string]*string
	if err := json.Unmarshal(raw, &secrets); err != nil {
		return nil, fmt.Errorf("secrets: %w", err)
	}
	return secrets, nil
}

const secretsHelp = "a file holding a JSON object of secrets, or - for stdin"

func instancesCmd() *cobra.Command {
	instances := &cobra.Command{
		Use:   "instances",
		Short: "List plugin instances, or show, create, configure, delete, or check one",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out, err := callAPI(cmd, "GET", "/instances", nil)
			if err != nil {
				return err
			}
			return show(cmd, out, printInstances)
		},
	}
	// get reads the instance args[0] names.
	get := func(cmd *cobra.Command, args []string) ([]byte, error) {
		id, err := parseID(args[0])
		if err != nil {
			return nil, err
		}
		return callAPI(cmd, "GET", "/instances/"+id, nil)
	}
	instances.AddCommand(&cobra.Command{
		Use:   "show <instance-id>",
		Short: "Show an instance's config and the names of its secrets",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := get(cmd, args)
			if err != nil {
				return err
			}
			return show(cmd, out, printInstance)
		},
	}, &cobra.Command{
		Use:   "check <instance-id>",
		Short: "Say whether an instance works, and fail if it's broken",
		Long:  "An instance is broken when its plugin failed to start with its settings. Configuring it clears that.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := get(cmd, args)
			if err != nil {
				return err
			}
			return show(cmd, out, func(w io.Writer, i instance) error {
				if i.BrokenError != nil {
					return fmt.Errorf("instance %d is broken: %s", i.ID, *i.BrokenError)
				}
				_, err := fmt.Fprintln(w, "ok")
				return err
			})
		},
	}, createInstanceCmd(), configureInstanceCmd(), &cobra.Command{
		Use:   "delete <instance-id>",
		Short: "Delete an instance; its jobs stay, and logins through it end",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			_, err = callAPI(cmd, "DELETE", "/instances/"+id, nil)
			return err
		},
	})
	return instances
}

func createInstanceCmd() *cobra.Command {
	var secrets, owner string
	create := &cobra.Command{
		Use:   "create <plugin> <name> [config-json]",
		Short: "Create a plugin instance",
		Long:  "Secrets come from a file or stdin, so they stay out of shell history. ink plugins lists each plugin's config schema with --json.",
		Example: `  ink instances create card-codec Codec
  ink instances create lumiverse Lumiverse '{"url": "https://lumiverse.example.com", "username": "owner"}' --secrets lumiverse.json`,
		Args: cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			config, err := jsonArg(args, 2, `{}`)
			if err != nil {
				return err
			}
			body := map[string]any{"plugin": args[0], "name": args[1], "config": config}
			if secrets != "" {
				if body["secrets"], err = readSecrets(cmd, secrets); err != nil {
					return err
				}
			}
			if cmd.Flags().Changed("owner-subject") {
				body["owner_subject"] = owner
			}
			out, err := callAPI(cmd, "POST", "/instances", body)
			if err != nil {
				return err
			}
			return show(cmd, out, printInstance)
		},
	}
	create.Flags().StringVar(&secrets, "secrets", "", secretsHelp)
	create.Flags().StringVar(&owner, "owner-subject", "", "for a login provider, the subject that logs in as the owner")
	return create
}

func configureInstanceCmd() *cobra.Command {
	var name, config, secrets, owner string
	configure := &cobra.Command{
		Use:   "configure <instance-id>",
		Short: "Change an instance's name, config, or secrets",
		Long: `Configure changes only what its flags name. --config replaces the whole config.
--secrets sets each secret it names, and a null value removes one. Configuring a
broken instance clears it, and its plugin restarts with the new settings.`,
		Example: `  ink instances configure 3 --config '{"url": "https://lumiverse.example.com"}'
  echo '{"password": null}' | ink instances configure 3 --secrets -`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			body := map[string]any{}
			if cmd.Flags().Changed("name") {
				body["name"] = name
			}
			if cmd.Flags().Changed("config") {
				if body["config"], err = jsonArg([]string{config}, 0, ""); err != nil {
					return err
				}
			}
			if cmd.Flags().Changed("secrets") {
				if body["secrets"], err = readSecrets(cmd, secrets); err != nil {
					return err
				}
			}
			if cmd.Flags().Changed("owner-subject") {
				body["owner_subject"] = owner
			}
			if len(body) == 0 {
				return errors.New("nothing to change; give --name, --config, --secrets, or --owner-subject")
			}
			out, err := callAPI(cmd, "PATCH", "/instances/"+id, body)
			if err != nil {
				return err
			}
			return show(cmd, out, printInstance)
		},
	}
	configure.Flags().StringVar(&name, "name", "", "the new name")
	configure.Flags().StringVar(&config, "config", "", "the whole new config, as JSON")
	configure.Flags().StringVar(&secrets, "secrets", "", secretsHelp)
	configure.Flags().StringVar(&owner, "owner-subject", "", "for a login provider, the owner's subject; empty logs the owner out")
	return configure
}
