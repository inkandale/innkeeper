package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/inkandale/innkeeper/internal/auth"
	"github.com/inkandale/innkeeper/internal/tier"
)

// token runs `innkeeper token create|list|revoke|revoke-grant` against the database, so
// the owner can make the first token before anyone can log in.
func token(args []string) error {
	ctx := context.Background()
	pool, err := openDB(ctx, 0)
	if err != nil {
		return err
	}
	defer pool.Close()
	a := auth.New(pool)
	switch {
	case (len(args) == 2 || len(args) == 3) && args[0] == "create":
		// Read-only unless asked, so a token made in passing can't change anything.
		t := tier.Read
		if len(args) == 3 {
			if t, err = tier.Parse(args[2]); err != nil {
				return err
			}
		}
		tok, secret, err := a.CreateToken(ctx, args[1], t)
		if err != nil {
			return err
		}
		fmt.Println(secret)
		fmt.Fprintf(os.Stderr, "created %s token %d; it is not shown again\n", tok.Tier, tok.ID)
		return nil
	case len(args) == 1 && args[0] == "list":
		list, err := a.Tokens(ctx)
		if err != nil {
			return err
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tNAME\tTIER\tCREATED\tLAST USED")
		for _, t := range list {
			used := "never"
			if t.LastUsedAt != nil {
				used = t.LastUsedAt.Local().Format(time.DateTime)
			}
			fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n", t.ID, t.Name, t.Tier, t.CreatedAt.Local().Format(time.DateTime), used)
		}
		grants, err := a.Grants(ctx)
		if err != nil {
			return err
		}
		if len(grants) > 0 {
			fmt.Fprintln(tw, "\nGRANT\tOAUTH CLIENT\tTIER\tAPPROVED\tLAST USED")
		}
		for _, g := range grants {
			used := "never"
			if g.LastUsedAt != nil {
				used = g.LastUsedAt.Local().Format(time.DateTime)
			}
			fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n", g.ID, g.ClientName, g.Tier, g.CreatedAt.Local().Format(time.DateTime), used)
		}
		return tw.Flush()
	case len(args) == 2 && (args[0] == "revoke" || args[0] == "revoke-grant"):
		id, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil {
			return fmt.Errorf("ID %q is not a number", args[1])
		}
		if args[0] == "revoke-grant" {
			return a.RevokeGrant(ctx, id)
		}
		return a.RevokeToken(ctx, id)
	}
	return errors.New(usage)
}
