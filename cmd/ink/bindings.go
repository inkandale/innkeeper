package main

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/davidgibbons/innkeeper/internal/wire"
)

// apiCall makes one API call with a new client.
func apiCall(cmd *cobra.Command, method, path string, body []byte) ([]byte, error) {
	c, err := newClient(cmd)
	if err != nil {
		return nil, err
	}
	return c.do(cmd.Context(), method, path, body)
}

func printBindings(w io.Writer, list []wire.Binding) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tINSTANCE\tREMOTE ID\tSTATUS\tLAST PUSH\tCHECKED")
	for _, b := range list {
		checked := "never"
		if b.CheckedAt != nil {
			checked = b.CheckedAt.Local().Format(time.DateTime)
		}
		fmt.Fprintf(tw, "%d\t%d\t%s\t%s\t%s %s\t%s\n", b.ID, b.InstanceID, b.RemoteID, b.Status,
			b.LastPush.Kind, b.LastPush.PushedAt.Local().Format(time.DateTime), checked)
	}
	return tw.Flush()
}

// printBinding prints the binding's row and the fields the app changed.
func printBinding(w io.Writer, b wire.Binding) error {
	if err := printBindings(w, []wire.Binding{b}); err != nil {
		return err
	}
	if len(b.Changes) > 0 {
		fmt.Fprintln(w, "\nchanged in the app since the last push or resolve:")
		printChanges(w, b.Changes, "pushed", "app")
	}
	return nil
}

// printChanges prints each field with its before and after values.
func printChanges(w io.Writer, changes []wire.Change, before, after string) {
	for _, c := range changes {
		fmt.Fprintf(w, "%s\n  %-7s %s\n  %-7s %s\n", c.Field, before+":", marked(c.Before, nil, "", "", ""),
			after+":", marked(c.After, nil, "", "", ""))
	}
}

func printPreview(w io.Writer, p wire.Preview) error {
	switch {
	case p.NewCopy:
		fmt.Fprintln(w, "the push makes another copy in the app")
	case p.Replaces:
		fmt.Fprintln(w, "the push replaces the app's copy")
	default:
		fmt.Fprintln(w, "the push makes a new copy in the app")
	}
	if p.Stale {
		fmt.Fprintln(w, "the app couldn't be read, so the changes compare against the last push")
	}
	if p.Replaces && len(p.Changes) == 0 {
		fmt.Fprintln(w, "it changes nothing there")
	}
	printChanges(w, p.Changes, "app", "pushed")
	if len(p.DroppedFields) > 0 {
		fmt.Fprintln(w, "dropped: "+strings.Join(p.DroppedFields, ", ")+" (the app doesn't store them)")
	}
	if len(p.CreateLorebooks) > 0 {
		fmt.Fprintf(w, "creates lorebooks in the app: %v\n", p.CreateLorebooks)
	}
	return nil
}

func printPushes(w io.Writer, list []wire.Push) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tKIND\tREVISION\tREMOTE ID\tPUSHED")
	for _, p := range list {
		fmt.Fprintf(tw, "%d\t%s\t%d\t%s\t%s\n", p.ID, p.Kind, p.RevisionID, p.RemoteID, p.PushedAt.Local().Format(time.DateTime))
	}
	return tw.Flush()
}

// idCmd is a command that takes one ID, calls method on path(id), and
// prints the response with table.
func idCmd[T any](use, short, method string, path func(string) string, table func(io.Writer, T) error) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			out, err := apiCall(cmd, method, path(id), nil)
			if err != nil {
				return err
			}
			return show(cmd, out, table)
		},
	}
}

func bindingsCmd() *cobra.Command {
	bindings := idCmd("bindings <card-id>", "List a card's bindings, or show one or its pushes", "GET",
		func(id string) string { return "/cards/" + id + "/bindings" }, printBindings)
	bindings.Long = "A binding links a card to its copy in one app. Its status says which side changed since the last push."
	bindings.AddCommand(
		idCmd("show <binding-id>", "Show a binding as last checked, with the fields the app changed", "GET",
			func(id string) string { return "/bindings/" + id }, printBinding),
		idCmd("pushes <binding-id>", "List a binding's pushes, newest first", "GET",
			func(id string) string { return "/bindings/" + id + "/pushes" }, printPushes),
	)
	return bindings
}

func statusCmd() *cobra.Command {
	return idCmd("status <binding-id>", "Read the app's copy now and show the binding's status", "POST",
		func(id string) string { return "/bindings/" + id + "/check" }, printBinding)
}

func previewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "preview <instance-id> <card-id>",
		Short: "Show what pushing a card would change in an app",
		Long:  "Preview reads the app's copy and prints each field the push would change, with the app's value and the pushed one.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			inst, err := parseID(args[0])
			if err != nil {
				return err
			}
			card, err := parseID(args[1])
			if err != nil {
				return err
			}
			out, err := apiCall(cmd, "GET", "/cards/"+card+"/push-preview?instance_id="+inst, nil)
			if err != nil {
				return err
			}
			return show(cmd, out, printPreview)
		},
	}
}

func unbindCmd() *cobra.Command {
	var yes bool
	unbind := &cobra.Command{
		Use:   "unbind <binding-id>",
		Short: "Forget a binding and its push history; the app keeps its copy",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			if err := confirm(cmd, yes, "Unbind binding "+id+" and delete its push history?"); err != nil {
				return err
			}
			_, err = apiCall(cmd, "DELETE", "/bindings/"+id, nil)
			return err
		},
	}
	unbind.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask first")
	return unbind
}
