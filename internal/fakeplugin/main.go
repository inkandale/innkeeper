// Command fakeplugin is a plugin for core tests. The -mode flag makes it
// misbehave in specific ways.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/inkandale/innkeeper/protocol"
)

func main() {
	mode := flag.String("mode", "", "bad-protocol | wrong-caps | wrong-version | slow-init | bad-errors | silent | crash-unknown | no-shutdown | bad-describe | bad-tools | bad-actions | bad-records | bad-enrich | bad-codec | codec-hard-link | codec-no-avatar | codec-wrong-code | database | bad-list | bad-setup | bad-catalog | catalog-repeat | catalog-lax | catalog-no-notfound | catalog-wrong-hash | catalog-sourceless | sites-noisy | sites-lax-data | sites-claims | sites-lax-params | sites-bad-source | no-delete | bad-put | no-tool-call | no-import | settings-no-defaults | settings-defaults-error | no-settings-run | bad-records-import | bad-decide | bad-vector | drop-tool-call")
	caps := flag.String("caps", "push,pull", "comma-separated capabilities to report")
	name := flag.String("name", "fake", "the plugin name to report")
	flag.Parse()

	var conn *protocol.Conn
	var blobTmp string
	var config map[string]any
	var databaseURL string
	// calls counts each method this process has answered, for the calls method.
	var callsMu sync.Mutex
	calls := map[string]int{}
	var deltas deltaLog
	// initChatErr is how the chat_on_initialize config's ai.chat failed.
	var initChatErr error
	handler := func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		callsMu.Lock()
		calls[method]++
		callsMu.Unlock()
		if config["down"] == true && strings.HasPrefix(method, "catalog.") && method != protocol.MethodCatalogDescribe {
			return nil, protocol.Fail("archive unavailable", true)
		}
		if config["failing"] == true && strings.HasPrefix(method, "catalog.") && method != protocol.MethodCatalogDescribe {
			return nil, protocol.Fail("open /srv/dump.sql: permission denied", false)
		}
		// As a consumer, the fake asks its provider from each call the contract
		// check drives; actions ask from actionRun.
		if id, ok := config["connection"].(float64); ok && slices.Contains(consumerCalls, method) {
			if err := chatWithTool(ctx, conn, method, int64(id), *mode); err != nil {
				return nil, err
			}
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
			res.Services = aiServices(init.Config)
			if id, ok := init.Config["chat_on_initialize"].(float64); ok {
				initChatErr = conn.Call(ctx, protocol.MethodAIChat, protocol.AIChatParams{
					ServiceCall: protocol.ServiceCall{Instance: int64(id)},
					Messages:    []protocol.ChatMessage{{Role: "user", Content: "hi"}},
				}, nil)
			}
			switch *mode {
			case "bad-protocol":
				res.Protocol = 99
			case "wrong-caps":
				res.Capabilities = []string{protocol.CapabilityPush}
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
		case "calls":
			var name string
			_ = json.Unmarshal(params, &name)
			callsMu.Lock()
			defer callsMu.Unlock()
			return calls[name], nil
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
			return nil, protocol.Fail("temporary failure", true)
		case "sleep":
			var ms int
			if err := json.Unmarshal(params, &ms); err != nil {
				return nil, protocol.InvalidParams(err)
			}
			time.Sleep(time.Duration(ms) * time.Millisecond)
			return "slept", nil
		case "crash":
			os.Exit(3)
		case protocol.MethodImportRun:
			if *mode == "no-import" {
				return nil, protocol.MethodNotFound(method)
			}
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
			if *mode == "no-tool-call" {
				return nil, protocol.MethodNotFound(method)
			}
			return mcpCall(params)
		case protocol.MethodActionList:
			return fakeActions(*mode), nil
		case protocol.MethodActionRun:
			return actionRun(ctx, conn, params, config, *mode)
		case protocol.MethodEnrichRun:
			return enrichRun(ctx, conn, params, *mode, config)
		case protocol.MethodRecordsList, protocol.MethodRecordsGet, protocol.MethodRecordsPut,
			protocol.MethodRecordsDelete, protocol.MethodRecordsImport:
			return recordsCall(method, params, *mode)
		case protocol.MethodTargetPut, protocol.MethodTargetPutLorebook:
			if *mode == "bad-put" {
				return protocol.TargetPutResult{}, nil
			}
			if method == protocol.MethodTargetPutLorebook {
				return newTarget(config).putLorebook(params)
			}
			return newTarget(config).put(params)
		case protocol.MethodTargetGet:
			if config["fail_get"] == "retryable" {
				return nil, protocol.Fail("app unavailable", true)
			}
			return newTarget(config).get(params, blobTmp)
		case protocol.MethodTargetGetLorebook:
			return newTarget(config).getLorebook(params)
		case protocol.MethodTargetDelete, protocol.MethodTargetDeleteLorebook:
			if *mode == "no-delete" {
				return nil, protocol.MethodNotFound(method)
			}
			return newTarget(config).remove(params)
		case protocol.MethodTargetList, protocol.MethodTargetListLorebooks:
			if config["fail_list"] == "retryable" {
				return nil, protocol.Fail("app unavailable", true)
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
			return catalogInfo, nil
		case protocol.MethodCatalogSearch:
			return catalogSearch(params, *mode)
		case protocol.MethodCatalogGet:
			return catalogGet(blobTmp, config, params, *mode)
		case protocol.MethodSettingsDefaults, protocol.MethodSettingsOptions, protocol.MethodSettingsRun:
			return settingsCall(method, params, *mode)
		case protocol.MethodAuthBegin:
			if *mode == "bad-setup" {
				return protocol.AuthBeginResult{Form: json.RawMessage(`{"type": "string"}`)}, nil
			}
			return protocol.AuthBeginResult{
				Form:  json.RawMessage(`{"type": "object", "properties": {"password": {"type": "string"}}, "required": ["password"]}`),
				State: "fake-state",
			}, nil
		case protocol.MethodAuthComplete:
			var p protocol.AuthCompleteParams
			_ = json.Unmarshal(params, &p)
			if p.State != "fake-state" {
				return nil, protocol.Fail("login state doesn't match", false)
			}
			switch p.Params["password"] {
			case "letmein":
				return protocol.AuthCompleteResult{Subject: "owner", Name: "Owner"}, nil
			case "stranger":
				return protocol.AuthCompleteResult{Subject: "someone-else"}, nil
			}
			return nil, protocol.Fail("wrong password", false)
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
				return nil, protocol.Fail("the passwords don't match", false)
			}
			return protocol.AuthSetupCompleteResult{
				Secrets: map[string]string{"password_hash": "fake:" + p.Params["password"]},
				Subject: "owner",
			}, nil
		case protocol.MethodAIChat:
			if base, ok := config["base_url"].(string); ok {
				return openAIChat(ctx, conn, base, params)
			}
			return aiChat(ctx, conn, params, func() {
				callsMu.Lock()
				calls["ai.chat.cancelled"]++
				callsMu.Unlock()
			})
		case protocol.MethodAIEmbed:
			return aiEmbed(params)
		case protocol.MethodVectorQuery:
			var p protocol.VectorQueryParams
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, protocol.InvalidParams(err)
			}
			if *mode == "bad-vector" {
				return protocol.VectorValue{Vector: []float32{1}}, nil
			}
			return protocol.VectorValue{Space: "fake", Vector: []float32{float32(len(p.Text)), 1}}, nil
		case protocol.MethodAIDecide:
			if *mode == "bad-decide" {
				return protocol.AIDecideResult{Model: "fake"}, nil
			}
			return aiDecide(params)
		case protocol.NotifyAIDelta:
			deltas.add(params)
			return nil, nil
		case "initialize-chat":
			// The error code of the ai.chat made during initialize, or 0.
			var rpcErr *protocol.Error
			if errors.As(initChatErr, &rpcErr) {
				return rpcErr.Code, nil
			}
			return 0, nil
		case "relay":
			return relay(ctx, conn, params, &deltas)
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

// consumerCalls are the calls a consumer fake asks its provider from.
var consumerCalls = []string{protocol.MethodMCPCall, protocol.MethodImportRun, protocol.MethodRecordsList, protocol.MethodSettingsRun}

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
	// Text is added to each card's description, so a second run changes them.
	Text string `json:"text"`
	// Version, Created, and Values go on each emit.card.
	Version string                     `json:"version"`
	Created time.Time                  `json:"created"`
	Values  map[string]json.RawMessage `json:"values"`
	// CheckpointEvery sends a checkpoint after every this many cards; 0 means 1.
	CheckpointEvery int `json:"checkpoint_every"`
	// NULCheckpoint sends the checkpoint with this next card holding a NUL,
	// which PostgreSQL refuses to store.
	NULCheckpoint int `json:"nul_checkpoint"`
}

func importRun(conn *protocol.Conn, blobTmp string, params json.RawMessage) (any, error) {
	var p protocol.ImportRunParams
	var opts importOptions
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, protocol.InvalidParams(err)
	}
	if err := json.Unmarshal(p.Params, &opts); err != nil {
		return nil, protocol.InvalidParams(err)
	}
	switch opts.Fail {
	case "retryable":
		return nil, protocol.Fail("source unavailable", true)
	case "fatal":
		return nil, protocol.Fail("no such folder", false)
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
		data := map[string]any{"name": fmt.Sprintf("Fake %d", i), "description": fmt.Sprintf("Card number %d.%s", i, opts.Text)}
		if opts.Source {
			data["source"] = []string{fmt.Sprintf("https://fake.example/u/card-%d", i)}
		}
		if opts.Book {
			data["character_book"] = map[string]any{"name": fmt.Sprintf("Lore %d", i), "entries": []any{map[string]any{"keys": []any{"lore"}, "content": "Lore."}}}
		}
		card := map[string]any{"spec": "chara_card_v3", "spec_version": "3.0", "data": data}
		if opts.Bad != nil && *opts.Bad == i {
			card["spec"] = "chara_card_v2"
		}
		raw, _ := json.Marshal(card)
		e := protocol.EmitCard{Run: p.Run, Item: fmt.Sprintf("card-%d", i), Card: raw, Version: opts.Version,
			Created: opts.Created, Values: opts.Values}
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
		if (i+1)%max(opts.CheckpointEvery, 1) == 0 {
			next, _ := json.Marshal(map[string]any{"next": i + 1})
			if opts.NULCheckpoint == i+1 {
				next, _ = json.Marshal(map[string]any{"next": i + 1, "note": "\u0000"})
			}
			_ = conn.Notify(protocol.NotifyCheckpoint, protocol.Checkpoint{Run: p.Run, Checkpoint: next})
		}
	}
	return struct{}{}, nil
}

// dbRoundTrip stores the string in params through database_url and reads it
// back, so a test can tell the plugin reached its own schema.
func dbRoundTrip(ctx context.Context, url string, params json.RawMessage) (any, error) {
	var v string
	if err := json.Unmarshal(params, &v); err != nil {
		return nil, protocol.InvalidParams(err)
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
