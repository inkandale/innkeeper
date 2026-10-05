// Command innkeeper is the Innkeeper server and CLI.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/davidgibbons/innkeeper/internal/contract"
)

const usage = `usage:
  innkeeper plugin check <plugin-dir>
  innkeeper migrate
  innkeeper worker
  innkeeper api
  innkeeper token create <name> [read|write|destructive] | list | revoke <id> | revoke-grant <id>
  innkeeper backup <dir>

API commands (read INNKEEPER_URL and INNKEEPER_TOKEN):
  innkeeper call <method> <path> [json]
  innkeeper import <instance-id> [params-json]
  innkeeper export <card-id> [file [format]]
  innkeeper bulk archive|unarchive <query-json>
  innkeeper push <instance-id> <card-id>|<query-json> [--new-copy]
  innkeeper jobs [show|cancel|watch <job-id>]

See README.md for the environment variables each command reads.`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "innkeeper:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	cmd, rest := args[0], args[1:]
	switch {
	case cmd == "plugin" && len(rest) == 2 && rest[0] == "check":
		return pluginCheck(rest[1])
	case cmd == "migrate" && len(rest) == 0:
		return migrate()
	case cmd == "worker" && len(rest) == 0:
		return worker()
	case cmd == "api" && len(rest) == 0:
		return apiServer()
	case cmd == "token":
		return token(rest)
	case cmd == "backup":
		return backup(rest)
	case cmd == "call":
		return call(rest)
	case cmd == "import":
		return importCmd(rest)
	case cmd == "export":
		return export(rest)
	case cmd == "bulk":
		return bulkCmd(rest)
	case cmd == "push":
		return pushCmd(rest)
	case cmd == "jobs":
		return jobsCmd(rest)
	}
	return errors.New(usage)
}

func pluginCheck(dir string) error {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	contract.DatabaseURL = os.Getenv("INNKEEPER_PLUGIN_DATABASE_URL")
	results, err := contract.Check(context.Background(), dir, log)
	if err != nil {
		return err
	}
	failed := 0
	for _, r := range results {
		if r.Err != nil {
			failed++
			fmt.Printf("FAIL  %s: %v\n", r.Name, r.Err)
		} else {
			fmt.Printf("PASS  %s\n", r.Name)
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d checks failed", failed, len(results))
	}
	return nil
}

func migrate() error {
	pool, err := openDB(context.Background(), 0)
	if err != nil {
		return err
	}
	pool.Close()
	return nil
}
