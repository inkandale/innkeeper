package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/davidgibbons/innkeeper/internal/auth"
)

// token runs `innkeeper token create|list|revoke` against the database, so
// the owner can make the first token before anyone can log in.
func token(args []string) error {
	ctx := context.Background()
	pool, err := openDB(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	a := auth.New(pool)
	switch {
	case len(args) == 2 && args[0] == "create":
		t, secret, err := a.CreateToken(ctx, args[1])
		if err != nil {
			return err
		}
		fmt.Println(secret)
		fmt.Fprintf(os.Stderr, "created token %d; it is not shown again\n", t.ID)
		return nil
	case len(args) == 1 && args[0] == "list":
		list, err := a.Tokens(ctx)
		if err != nil {
			return err
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tNAME\tCREATED\tLAST USED")
		for _, t := range list {
			used := "never"
			if t.LastUsedAt != nil {
				used = t.LastUsedAt.Local().Format(time.DateTime)
			}
			fmt.Fprintf(tw, "%d\t%s\t%s\t%s\n", t.ID, t.Name, t.CreatedAt.Local().Format(time.DateTime), used)
		}
		return tw.Flush()
	case len(args) == 2 && args[0] == "revoke":
		id, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil {
			return fmt.Errorf("token ID %q is not a number", args[1])
		}
		return a.RevokeToken(ctx, id)
	}
	return errors.New(usage)
}
