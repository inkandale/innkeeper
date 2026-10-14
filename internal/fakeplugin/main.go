// Command fakeplugin is a plugin for core tests. The -mode flag makes it
// misbehave in specific ways.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/davidgibbons/innkeeper/protocol"
)

func main() {
	mode := flag.String("mode", "", "bad-protocol | wrong-caps | wrong-version | slow-init | bad-errors | silent | crash-unknown | no-shutdown | bad-describe | bad-tools | bad-codec | codec-hard-link | codec-no-avatar | codec-wrong-code | database | bad-list | bad-setup | bad-catalog | catalog-repeat | catalog-lax | catalog-no-notfound | catalog-wrong-hash | catalog-sourceless | sites-noisy | sites-lax-data | sites-claims | sites-lax-params | sites-bad-source")
	caps := flag.String("caps", "push,pull", "comma-separated capabilities to report")
	name := flag.String("name", "fake", "the plugin name to report")
	flag.Parse()

	var conn *protocol.Conn
	var blobTmp string
	var config map[string]any
	var databaseURL string
	handler := func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		if config["down"] == true && strings.HasPrefix(method, "catalog.") && method != protocol.MethodCatalogDescribe {
			return nil, protocol.NewError(-32000, "archive unavailable", true)
		}
		if config["failing"] == true && strings.HasPrefix(method, "catalog.") && method != protocol.MethodCatalogDescribe {
			return nil, protocol.NewError(-32000, "open /srv/dump.sql: permission denied", false)
		}
		switch method {
		case protocol.MethodInitialize:
			var init protocol.InitializeParams
			_ = json.Unmarshal(params, &init)
			blobTmp, config, databaseURL = init.BlobTmp, init.Config, init.DatabaseURL
			res := protocol.InitializeResult{
				Protocol:     protocol.Version,
				Name:         *name,
				Version:      "0.0.1",
				Capabilities: strings.Split(*caps, ","),
			}
			switch *mode {
			case "bad-protocol":
				res.Protocol = 99
			case "wrong-caps":
				res.Capabilities = []string{"push"}
			case "wrong-version":
				res.Version = "9.9.9"
			case "slow-init":
				time.Sleep(time.Minute)
			}
			return res, nil
		case protocol.MethodShutdown:
			if *mode == "no-shutdown" {
				time.Sleep(time.Minute)
			}
			return nil, nil
		case "echo":
			return params, nil
		case "shout":
			fmt.Fprintf(os.Stderr, "%s\nafter\n", strings.Repeat("x", 100_000))
			return nil, nil
		case "env":
			var name string
			_ = json.Unmarshal(params, &name)
			return os.Getenv(name), nil
		case "db":
			return dbRoundTrip(ctx, databaseURL, params)
		case "callback":
			var out string
			err := conn.Call(ctx, "core.ping", nil, &out)
			return out, err
		case "fail":
			return nil, protocol.NewError(-32000, "temporary failure", true)
		case "sleep":
			var ms int
			if err := json.Unmarshal(params, &ms); err != nil {
				return nil, protocol.NewError(protocol.CodeInvalidParams, err.Error(), false)
			}
			time.Sleep(time.Duration(ms) * time.Millisecond)
			return "slept", nil
		case "crash":
			os.Exit(3)
		case protocol.MethodImportRun:
			return importRun(conn, blobTmp, params)
		case protocol.MethodTargetDescribe:
			if *mode == "bad-describe" {
				return protocol.TargetInfo{Lorebooks: "sometimes"}, nil
			}
			return newTarget(config).describe(), nil
		case protocol.MethodMCPTools:
			if config["hang_tools"] == true {
				time.Sleep(time.Minute)
			}
			return mcpTools(*mode), nil
		case protocol.MethodMCPCall:
			return mcpCall(params)
		case protocol.MethodTargetPut:
			return newTarget(config).put(params)
		case protocol.MethodTargetGet:
			if config["fail_get"] == "retryable" {
				return nil, protocol.NewError(-32000, "app unavailable", true)
			}
			return newTarget(config).get(params, blobTmp)
		case protocol.MethodTargetPutLorebook:
			return newTarget(config).putLorebook(params)
		case protocol.MethodTargetGetLorebook:
			return newTarget(config).getLorebook(params)
		case protocol.MethodTargetList, protocol.MethodTargetListLorebooks:
			if config["fail_list"] == "retryable" {
				return nil, protocol.NewError(-32000, "app unavailable", true)
			}
			if *mode == "bad-list" {
				return protocol.TargetListResult{Items: []protocol.TargetItem{{Name: "No ID"}}}, nil
			}
			prefix := "r"
			if method == protocol.MethodTargetListLorebooks {
				prefix = "l"
			}
			return newTarget(config).list(prefix)
		case protocol.MethodCodecEncode:
			return codecEncode(blobTmp, params, *mode)
		case protocol.MethodCodecDecode:
			return codecDecode(params, *mode)
		case protocol.MethodSitesDetect:
			return sitesDetect(config, params, *mode)
		case protocol.MethodSitesParse:
			return sitesParse(config, params, *mode)
		case protocol.MethodCatalogDescribe:
			if *mode == "bad-catalog" {
				return protocol.CatalogInfo{}, nil
			}
			info := catalogInfo
			info.Ingest = config["no_ingest"] != true
			return info, nil
		case protocol.MethodCatalogIngest:
			return catalogIngest(conn, config, params)
		case protocol.MethodCatalogSearch:
			return catalogSearch(params, *mode)
		case protocol.MethodCatalogGet:
			return catalogGet(blobTmp, config, params, *mode)
		case protocol.MethodCatalogSimilar:
			return catalogSimilar(params, *mode)
		case protocol.MethodAuthBegin:
			return protocol.AuthBeginResult{
				Form:  json.RawMessage(`{"type": "object", "properties": {"password": {"type": "string"}}, "required": ["password"]}`),
				State: "fake-state",
			}, nil
		case protocol.MethodAuthComplete:
			var p protocol.AuthCompleteParams
			_ = json.Unmarshal(params, &p)
			if p.State != "fake-state" {
				return nil, protocol.NewError(-32000, "login state doesn't match", false)
			}
			switch p.Params["password"] {
			case "letmein":
				return protocol.AuthCompleteResult{Subject: "owner", Name: "Owner"}, nil
			case "stranger":
				return protocol.AuthCompleteResult{Subject: "someone-else"}, nil
			}
			return nil, protocol.NewError(-32000, "wrong password", false)
		case protocol.MethodAuthSetup:
			if *mode == "bad-setup" {
				return protocol.AuthSetupResult{}, nil
			}
			return protocol.AuthSetupResult{
				Form: json.RawMessage(`{"type": "object", "properties": {"password": {"type": "string"}, "confirm": {"type": "string"}}, "required": ["password", "confirm"]}`),
			}, nil
		case protocol.MethodAuthSetupComplete:
			var p protocol.AuthSetupCompleteParams
			_ = json.Unmarshal(params, &p)
			if p.Params["password"] == "" || p.Params["password"] != p.Params["confirm"] {
				return nil, protocol.NewError(-32000, "the passwords don't match", false)
			}
			return protocol.AuthSetupCompleteResult{
				Secrets: map[string]string{"password_hash": "fake:" + p.Params["password"]},
				Subject: "owner",
			}, nil
		case "freeze":
			// A notification handler blocks the reader, so the plugin
			// stops reading stdin, like a hung plugin.
			time.Sleep(time.Minute)
		}
		switch *mode {
		case "bad-errors":
			return nil, nil
		case "silent":
			time.Sleep(time.Minute)
			return nil, nil
		case "crash-unknown":
			os.Exit(4)
		}
		return nil, protocol.MethodNotFound(method)
	}

	conn = protocol.NewConn(os.Stdin, os.Stdout, handler)
	if err := conn.Run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *mode == "no-shutdown" {
		time.Sleep(time.Minute)
	}
}

// importOptions are the fake importer's params.
type importOptions struct {
	Cards int `json:"cards"`
	// CrashAt exits after emitting this card, before its checkpoint, unless
	// the run resumed from a checkpoint.
	CrashAt *int `json:"crash_at"`
	// Bad emits this card with a spec the core rejects.
	Bad *int `json:"bad"`
	// Lorebooks is how many standalone lorebooks to emit before the cards.
	Lorebooks int  `json:"lorebooks"`
	Book      bool `json:"book"`
	Avatar    bool `json:"avatar"`
	// Source gives each card a fake site page in data.source.
	Source bool   `json:"source"`
	Fail   string `json:"fail"` // retryable or fatal
}

func importRun(conn *protocol.Conn, blobTmp string, params json.RawMessage) (any, error) {
	var p protocol.ImportRunParams
	var opts importOptions
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, protocol.NewError(protocol.CodeInvalidParams, err.Error(), false)
	}
	if err := json.Unmarshal(p.Params, &opts); err != nil {
		return nil, protocol.NewError(protocol.CodeInvalidParams, err.Error(), false)
	}
	switch opts.Fail {
	case "retryable":
		return nil, protocol.NewError(-32000, "source unavailable", true)
	case "fatal":
		return nil, protocol.NewError(-32000, "no such folder", false)
	}
	var cp struct {
		Next int `json:"next"`
	}
	resumed := len(p.Checkpoint) > 0 && string(p.Checkpoint) != "null"
	if resumed {
		_ = json.Unmarshal(p.Checkpoint, &cp)
	}
	for i := range opts.Lorebooks {
		if resumed {
			break
		}
		book, _ := json.Marshal(map[string]any{"name": fmt.Sprintf("World %d", i), "entries": []any{}})
		_ = conn.Notify(protocol.NotifyEmitLorebook, protocol.EmitLorebook{Run: p.Run, Item: fmt.Sprintf("world-%d", i), Lorebook: book})
	}
	for i := cp.Next; i < opts.Cards; i++ {
		data := map[string]any{"name": fmt.Sprintf("Fake %d", i), "description": fmt.Sprintf("Card number %d.", i)}
		if opts.Source {
			data["source"] = []string{fmt.Sprintf("https://fake.example/u/card-%d", i)}
		}
		if opts.Book {
			data["character_book"] = map[string]any{"name": fmt.Sprintf("Lore %d", i), "entries": []any{}}
		}
		card := map[string]any{"spec": "chara_card_v3", "spec_version": "3.0", "data": data}
		if opts.Bad != nil && *opts.Bad == i {
			card["spec"] = "chara_card_v2"
		}
		raw, _ := json.Marshal(card)
		e := protocol.EmitCard{Run: p.Run, Item: fmt.Sprintf("card-%d", i), Card: raw}
		if opts.Avatar {
			e.Avatar = filepath.Join(blobTmp, fmt.Sprintf("avatar-%d", i))
			if err := os.WriteFile(e.Avatar, fmt.Appendf(nil, "avatar %d", i), 0o644); err != nil {
				return nil, err
			}
		}
		_ = conn.Notify(protocol.NotifyEmitCard, e)
		if opts.CrashAt != nil && *opts.CrashAt == i && !resumed {
			os.Exit(5)
		}
		_ = conn.Notify(protocol.NotifyProgress, protocol.Progress{Run: p.Run, Done: int64(i + 1), Total: int64(opts.Cards)})
		next, _ := json.Marshal(map[string]int{"next": i + 1})
		_ = conn.Notify(protocol.NotifyCheckpoint, protocol.Checkpoint{Run: p.Run, Checkpoint: next})
	}
	return struct{}{}, nil
}

// dbRoundTrip stores the string in params through database_url and reads it
// back, so a test can tell the plugin reached its own schema.
func dbRoundTrip(ctx context.Context, url string, params json.RawMessage) (any, error) {
	var v string
	if err := json.Unmarshal(params, &v); err != nil {
		return nil, protocol.NewError(protocol.CodeInvalidParams, err.Error(), false)
	}
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return nil, err
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS note (v text)`); err != nil {
		return nil, err
	}
	var got string
	err = conn.QueryRow(ctx, `INSERT INTO note VALUES ($1) RETURNING v`, v).Scan(&got)
	return got, err
}
