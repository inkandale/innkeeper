// Command innkeeper is the Innkeeper server and CLI.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"

	"github.com/inkandale/innkeeper/internal/contract"
)

const usage = `usage:
  innkeeper plugin check <plugin-dir>
  innkeeper migrate
  innkeeper worker
  innkeeper api
  innkeeper serve
  innkeeper openapi
  innkeeper token create <name> [read|write|destructive] | list | revoke <id> | revoke-grant <id>
  innkeeper backup <dir> [assets|config]
  innkeeper restore [--replace] <archive-or-backup-dir>...

The API commands moved to the ink client; see docs/cli.md.`

// version is set at build with -ldflags "-X main.version=...".
var version = "dev"

// buildVersion is the version set at build, or the module version for go install.
func buildVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok && version == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return version
}

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
	case cmd == "serve" && len(rest) == 0:
		return serve()
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
	opts := contract.Options{
		DatabaseURL:    os.Getenv("INNKEEPER_PLUGIN_DATABASE_URL"),
		Provider:       os.Getenv("INNKEEPER_PROVIDER_PLUGIN"),
		ProviderConfig: os.Getenv("INNKEEPER_PROVIDER_CONFIG"),
	}
	if s := os.Getenv("INNKEEPER_PLUGIN_CONFIG"); s != "" {
		if err := json.Unmarshal([]byte(s), &opts.Config); err != nil {
			return fmt.Errorf("INNKEEPER_PLUGIN_CONFIG: %w", err)
		}
	}
	results, err := contract.Check(context.Background(), dir, opts, log)
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
