// Command innkeeper is the Innkeeper server and CLI.
package main

import (
	"context"
	"encoding/json"
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
  innkeeper openapi
  innkeeper token create <name> [read|write|destructive] | list | revoke <id> | revoke-grant <id>
  innkeeper backup <dir> [assets|config]
  innkeeper restore [--replace] <archive-or-backup-dir>...

The API commands moved to the ink client; see README.md.`

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
	case cmd == "openapi" && len(rest) == 0:
		return openapi()
	case cmd == "token":
		return token(rest)
	case cmd == "backup":
		return backupCmd(rest)
	case cmd == "restore":
		return restoreCmd(rest)
	}
	return errors.New(usage)
}

func pluginCheck(dir string) error {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	contract.DatabaseURL = os.Getenv("INNKEEPER_PLUGIN_DATABASE_URL")
	contract.Provider = os.Getenv("INNKEEPER_PROVIDER_PLUGIN")
	contract.ProviderConfig = os.Getenv("INNKEEPER_PROVIDER_CONFIG")
	if s := os.Getenv("INNKEEPER_PLUGIN_CONFIG"); s != "" {
		if err := json.Unmarshal([]byte(s), &contract.Config); err != nil {
			return fmt.Errorf("INNKEEPER_PLUGIN_CONFIG: %w", err)
		}
	}
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
