// Package contract checks that a plugin follows the plugin protocol. The
// innkeeper-plugins CI runs it against every plugin via `innkeeper plugin
// check`.
package contract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/protocol"
)

// callTimeout bounds each check's call so a silent plugin can't hang CI. A
// variable so tests can shorten it.
var callTimeout = 5 * time.Second

// DatabaseURL is sent to the plugin as database_url, for a plugin that
// declares `database = true`. Empty sends none.
var DatabaseURL string

// Config is sent to the plugin as its instance config. Nil sends an empty
// one.
var Config map[string]any

// Result is the outcome of one check. Err is nil when it passed.
type Result struct {
	Name string
	Err  error
}

// Check runs every check against the plugin in dir. It returns an error only
// when the manifest can't be loaded, since nothing else can be checked then.
func Check(ctx context.Context, dir string, log *slog.Logger) ([]Result, error) {
	m, err := protocol.LoadManifest(dir)
	if err != nil {
		return nil, err
	}
	// A theme has no process; loading the manifest checked its stylesheet.
	if m.IsTheme() {
		return []Result{{"theme stylesheet is readable", nil}}, nil
	}
	secrets := checkSecrets(m)
	// root is the blob store; a codec writes its files to blob_tmp, which is
	// <root>/tmp as in a real store.
	root, err := os.MkdirTemp("", "innkeeper-check-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(root)
	tmp := filepath.Join(root, "tmp")
	if err := os.Mkdir(tmp, 0o700); err != nil {
		return nil, err
	}
	config := Config
	if config == nil {
		config = map[string]any{}
	}
	params := protocol.InitializeParams{InstanceID: "contract-check", Config: config, Secrets: secrets,
		BlobStores: map[string]string{"contract-check": root}, BlobTmp: tmp, DatabaseURL: DatabaseURL}

	p, err := pluginhost.Start(ctx, m, params, noCore, log)
	results := []Result{{"initialize handshake matches manifest", err}}
	if err != nil {
		return results, nil
	}
	callCtx, cancel := context.WithTimeout(ctx, callTimeout)
	err = p.Call(callCtx, "innkeeper.contract.unknown", nil, nil)
	cancel()
	results = append(results, Result{"unknown method returns -32601", expectCode(err, protocol.CodeMethodNotFound)})

	// A plugin that already died would "exit after shutdown" without
	// handling it.
	if dead(err, p) {
		return exited(results, p), nil
	}

	suites := []struct {
		capabilities []string
		run          func() []Result
	}{
		{[]string{"push", "pull"}, func() []Result { return checkTarget(ctx, p, m) }},
		{[]string{"auth"}, func() []Result { return []Result{{"auth.setup returns a form or -32601", checkSetup(ctx, p)}} }},
		{[]string{"mcp"}, func() []Result { return []Result{{"mcp.tools returns valid tools", checkTools(ctx, p)}} }},
		{[]string{"action"}, func() []Result { return checkActions(ctx, p) }},
		{[]string{"records"}, func() []Result { return checkRecords(ctx, p, m) }},
		{[]string{"enrich"}, func() []Result { return []Result{{checkEnrichName, checkEnrich(ctx, p, m)}} }},
		{[]string{"ai"}, func() []Result { return []Result{{checkServicesName, checkServices(ctx, p)}} }},
		{[]string{"catalog"}, func() []Result { return checkCatalog(ctx, p, tmp) }},
		{[]string{"sites"}, func() []Result { return checkSites(ctx, p) }},
		{[]string{"codec"}, func() []Result {
			return runChecks(ctx, p, []check{
				{checkRoundTripName, func(ctx context.Context) error { return checkRoundTrip(ctx, p, m, tmp) }},
				{checkRejectsName, func(ctx context.Context) error { return checkRejects(ctx, p, tmp) }},
			})
		}},
	}
	for _, suite := range suites {
		if !slices.ContainsFunc(suite.capabilities, m.Has) {
			continue
		}
		results = append(results, suite.run()...)
		if dead(results[len(results)-1].Err, p) {
			return exited(results, p), nil
		}
	}

	if len(m.Uses) > 0 {
		results = append(results, checkUses(ctx, m, params, log)...)
	}

	callCtx, cancel = context.WithTimeout(ctx, callTimeout)
	err = p.Call(callCtx, protocol.MethodShutdown, nil, nil)
	cancel()
	if errors.Is(err, context.DeadlineExceeded) {
		err = fmt.Errorf("no response within %s", callTimeout)
	}
	results = append(results, Result{"answers shutdown", err})
	return append(results, Result{"exits after shutdown", p.Stop()}), nil
}

// checkSecrets gives each secret the manifest declares a placeholder value.
func checkSecrets(m *protocol.Manifest) map[string]string {
	secrets := map[string]string{}
	for _, s := range m.Secrets {
		secrets[s] = "contract-check"
	}
	return secrets
}

// check is one named check against a running plugin.
type check struct {
	name string
	run  func(context.Context) error
}

// runChecks runs checks in order, stopping if the plugin dies.
func runChecks(ctx context.Context, p *pluginhost.Process, checks []check) []Result {
	var results []Result
	for _, c := range checks {
		err := c.run(ctx)
		results = append(results, Result{c.name, err})
		if dead(err, p) {
			break
		}
	}
	return results
}

// dead reports whether the plugin is gone, so no further check can run.
func dead(err error, p *pluginhost.Process) bool {
	return errors.Is(err, protocol.ErrClosed) || !p.Alive()
}

const checkGoneName = "a pull-only target answers -32004 for an item it doesn't have"

// checkTarget checks target.describe and, for a pull target, its lists. A
// pull-only target follows site sources, so it must also say when one is gone.
func checkTarget(ctx context.Context, p *pluginhost.Process, m *protocol.Manifest) []Result {
	info, err := checkDescribe(ctx, p, m)
	results := []Result{{"target.describe returns a valid description", err}}
	if err != nil || dead(err, p) {
		return results
	}
	if info.Delete {
		err = checkDelete(ctx, p, info)
		results = append(results, Result{"a target that claims delete answers target.delete", err})
		if dead(err, p) {
			return results
		}
	}
	if !info.Pull {
		return results
	}
	err = checkLists(ctx, p)
	results = append(results, Result{"a pull target answers target.list and target.list_lorebooks", err})
	if m.Has("push") || dead(err, p) {
		return results
	}
	return append(results, Result{checkGoneName, checkGone(ctx, p, info)})
}

// checkGone asks for a card, and a lorebook if the target keeps standalone
// ones, that no site has. The core shows -32004 as "no longer on the site", and
// anything else as a failed check.
func checkGone(ctx context.Context, p *pluginhost.Process, info protocol.TargetInfo) error {
	methods := []string{protocol.MethodTargetGet}
	if info.Lorebooks == protocol.LorebooksStandalone {
		methods = append(methods, protocol.MethodTargetGetLorebook)
	}
	for _, method := range methods {
		err := call(ctx, p, method, protocol.TargetGetParams{RemoteID: "innkeeper-contract:none"}, nil)
		if err := expectCode(err, protocol.CodeRemoteNotFound); err != nil {
			return fmt.Errorf("%s: %w", method, err)
		}
	}
	return nil
}

// checkDelete deletes an item no app has, and a lorebook too if the target
// keeps standalone ones. Without its app the target can only fail, so any
// error but -32601 counts as an answer.
func checkDelete(ctx context.Context, p *pluginhost.Process, info protocol.TargetInfo) error {
	methods := []string{protocol.MethodTargetDelete}
	if info.Lorebooks == protocol.LorebooksStandalone {
		methods = append(methods, protocol.MethodTargetDeleteLorebook)
	}
	for _, method := range methods {
		err := call(ctx, p, method, protocol.TargetDeleteParams{RemoteID: "innkeeper-contract:none"}, nil)
		if err != nil && !answered(err) {
			return fmt.Errorf("%s: %w", method, err)
		}
	}
	return nil
}

// checkTmpFile reports why path isn't a file the core would take from blob_tmp.
func checkTmpFile(path, tmp string) error {
	if !inside(path, tmp) {
		return fmt.Errorf("%q is not directly inside blob_tmp (%s)", path, tmp)
	}
	return plainFile(path)
}

// exited stops a plugin that died mid-check and fails the shutdown checks.
func exited(results []Result, p *pluginhost.Process) []Result {
	_ = p.Stop()
	dead := errors.New("plugin had already exited before shutdown")
	return append(results, Result{"answers shutdown", dead}, Result{"exits after shutdown", dead})
}

func checkDescribe(ctx context.Context, p *pluginhost.Process, m *protocol.Manifest) (protocol.TargetInfo, error) {
	var info protocol.TargetInfo
	if err := call(ctx, p, protocol.MethodTargetDescribe, nil, &info); err != nil {
		return info, err
	}
	if err := info.Validate(); err != nil {
		return info, err
	}
	if info.Pull && !m.Has("pull") {
		return info, errors.New("pull is true, but the manifest doesn't declare the pull capability")
	}
	return info, nil
}

// checkLists calls both list methods and wants every item to have a
// remote_id. The check runs with an empty config, so a target that needs its
// app may only return an error, which still counts as an answer.
func checkLists(ctx context.Context, p *pluginhost.Process) error {
	for _, method := range []string{protocol.MethodTargetList, protocol.MethodTargetListLorebooks} {
		var res protocol.TargetListResult
		var rpcErr *protocol.Error
		err := call(ctx, p, method, nil, &res)
		if errors.As(err, &rpcErr) && rpcErr.Code != protocol.CodeMethodNotFound {
			continue
		}
		if err != nil {
			return fmt.Errorf("%s: %w", method, err)
		}
		for _, it := range res.Items {
			if it.RemoteID == "" {
				return fmt.Errorf("%s: item %q has no remote_id", method, it.Name)
			}
		}
	}
	return nil
}

func checkTools(ctx context.Context, p *pluginhost.Process) error {
	var res protocol.MCPToolsResult
	if err := call(ctx, p, protocol.MethodMCPTools, nil, &res); err != nil {
		return err
	}
	seen := map[string]bool{}
	var errs []error
	for _, t := range res.Tools {
		if seen[t.Name] {
			errs = append(errs, fmt.Errorf("tool name %q repeats", t.Name))
		}
		seen[t.Name] = true
		errs = append(errs, t.Validate())
	}
	return errors.Join(errs...)
}

// checkSetup accepts a plugin without setup, or one whose form is a JSON
// object.
func checkSetup(ctx context.Context, p *pluginhost.Process) error {
	var res protocol.AuthSetupResult
	err := call(ctx, p, protocol.MethodAuthSetup, nil, &res)
	var rpcErr *protocol.Error
	if errors.As(err, &rpcErr) && rpcErr.Code == protocol.CodeMethodNotFound {
		return nil
	}
	if err != nil {
		return err
	}
	var form map[string]any
	if err := json.Unmarshal(res.Form, &form); err != nil || form == nil {
		return errors.New("form is not a JSON object")
	}
	return nil
}

// call makes one plugin call with its own timeout.
func call(ctx context.Context, p *pluginhost.Process, method string, params, result any) error {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	return callErr(p.Call(ctx, method, params, result))
}

// inside reports whether path is directly in dir, resolving symlinks the way
// the core does.
func inside(path, dir string) bool {
	d, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return false
	}
	want, err := filepath.EvalSymlinks(dir)
	return err == nil && d == want
}

// plainFile reports why the core's blob store would refuse the file at path:
// it takes only regular files with one link.
func plainFile(path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%q is not a regular file", path)
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); !ok || uint64(st.Nlink) != 1 {
		return fmt.Errorf("%q has other hard links", path)
	}
	return nil
}

// callErr explains a call that timed out or found the plugin gone.
func callErr(err error) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("no response within %s", callTimeout)
	case errors.Is(err, protocol.ErrClosed):
		return fmt.Errorf("plugin exited: %w", err)
	}
	return err
}

const (
	checkRoundTripName = "codec round-trips a card in every format"
	checkRejectsName   = "codec.decode rejects a file that isn't a card"
)

var sampleCard = json.RawMessage(`{"spec": "chara_card_v3", "spec_version": "3.0", "data": {"name": "Contract Check", "description": "A card for the codec check.", "first_mes": "Hello."}}`)

// checkRoundTrip encodes the sample card in each format and decodes it back.
func checkRoundTrip(ctx context.Context, p *pluginhost.Process, m *protocol.Manifest, tmp string) error {
	// Real exports usually carry an avatar. It sits beside blob_tmp, as the
	// core's stored blobs do.
	avatar := filepath.Join(filepath.Dir(tmp), "avatar.png")
	f, err := os.Create(avatar)
	if err != nil {
		return err
	}
	err = png.Encode(f, image.NewRGBA(image.Rect(0, 0, 1, 1)))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	for _, format := range m.Formats {
		var enc protocol.CodecEncodeResult
		if err := call(ctx, p, protocol.MethodCodecEncode, protocol.CodecEncodeParams{Card: sampleCard, Format: format, Avatar: avatar}, &enc); err != nil {
			return fmt.Errorf("encode %s: %w", format, err)
		}
		if err := checkTmpFile(enc.Path, tmp); err != nil {
			return fmt.Errorf("encode %s: path: %w", format, err)
		}
		if !protocol.ValidExtension(enc.Extension) {
			return fmt.Errorf("encode %s: extension %q must be 1 to 10 lowercase letters and digits", format, enc.Extension)
		}
		var dec protocol.CodecDecodeResult
		if err := call(ctx, p, protocol.MethodCodecDecode, protocol.CodecDecodeParams{Path: enc.Path}, &dec); err != nil {
			return fmt.Errorf("decode %s: %w", format, err)
		}
		var c struct {
			Spec string `json:"spec"`
			Data struct {
				Name string `json:"name"`
			} `json:"data"`
		}
		if err := json.Unmarshal(dec.Card, &c); err != nil || c.Spec != "chara_card_v3" || c.Data.Name != "Contract Check" {
			return fmt.Errorf("decode %s: got %s, want a chara_card_v3 card named Contract Check", format, dec.Card)
		}
		if dec.Avatar != "" {
			if err := checkTmpFile(dec.Avatar, tmp); err != nil {
				return fmt.Errorf("decode %s: avatar: %w", format, err)
			}
		}
	}
	return nil
}

// checkRejects expects an error reply, not a card, for a text file.
func checkRejects(ctx context.Context, p *pluginhost.Process, tmp string) error {
	path := filepath.Join(tmp, "not-a-card.txt")
	if err := os.WriteFile(path, []byte("This is not a character card.\n"), 0o644); err != nil {
		return err
	}
	err := call(ctx, p, protocol.MethodCodecDecode, protocol.CodecDecodeParams{Path: path}, nil)
	var rpcErr *protocol.Error
	switch {
	case err == nil:
		return errors.New("codec.decode accepted a text file; reply with a JSON-RPC error when the file isn't a card")
	case errors.As(err, &rpcErr) && rpcErr.Code != protocol.CodeInvalidParams:
		return fmt.Errorf("codec.decode replied %d; reply -32602 when the file isn't a card", rpcErr.Code)
	case errors.As(err, &rpcErr):
		return nil
	}
	return err
}

// noCore answers plugin callbacks during a check, where no library exists.
func noCore(_ context.Context, method string, _ json.RawMessage) (any, error) {
	return nil, protocol.MethodNotFound(method)
}

func expectCode(err error, code int) error {
	if err == nil {
		return fmt.Errorf("expected error %d, got success", code)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("expected error %d, got no response within %s", code, callTimeout)
	}
	if errors.Is(err, protocol.ErrClosed) {
		return fmt.Errorf("expected error %d, but the plugin exited", code)
	}
	var rpcErr *protocol.Error
	if !errors.As(err, &rpcErr) {
		return fmt.Errorf("expected error %d, got %w", code, err)
	}
	if rpcErr.Code != code {
		return fmt.Errorf("expected error %d, got %d", code, rpcErr.Code)
	}
	return nil
}
