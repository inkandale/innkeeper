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
	secrets := map[string]string{}
	for _, s := range m.Secrets {
		secrets[s] = "contract-check"
	}
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
	params := protocol.InitializeParams{InstanceID: "contract-check", Config: map[string]any{}, Secrets: secrets,
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
	if errors.Is(err, protocol.ErrClosed) || !p.Alive() {
		return exited(results, p), nil
	}

	if slices.Contains(m.Capabilities, "push") {
		descErr := checkDescribe(ctx, p, m)
		results = append(results, Result{"target.describe returns a valid description", descErr})
		if errors.Is(descErr, protocol.ErrClosed) || !p.Alive() {
			return exited(results, p), nil
		}
	}

	if slices.Contains(m.Capabilities, "mcp") {
		toolsErr := checkTools(ctx, p)
		results = append(results, Result{"mcp.tools returns valid tools", toolsErr})
		if errors.Is(toolsErr, protocol.ErrClosed) || !p.Alive() {
			return exited(results, p), nil
		}
	}

	if slices.Contains(m.Capabilities, "codec") {
		checks := []struct {
			name string
			run  func(context.Context) error
		}{
			{checkRoundTripName, func(ctx context.Context) error { return checkRoundTrip(ctx, p, m, tmp) }},
			{checkRejectsName, func(ctx context.Context) error { return checkRejects(ctx, p, tmp) }},
		}
		for _, c := range checks {
			err := c.run(ctx)
			results = append(results, Result{c.name, err})
			if errors.Is(err, protocol.ErrClosed) || !p.Alive() {
				return exited(results, p), nil
			}
		}
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

// exited stops a plugin that died mid-check and fails the shutdown checks.
func exited(results []Result, p *pluginhost.Process) []Result {
	_ = p.Stop()
	dead := errors.New("plugin had already exited before shutdown")
	return append(results, Result{"answers shutdown", dead}, Result{"exits after shutdown", dead})
}

func checkDescribe(ctx context.Context, p *pluginhost.Process, m *protocol.Manifest) error {
	var info protocol.TargetInfo
	if err := call(ctx, p, protocol.MethodTargetDescribe, nil, &info); err != nil {
		return err
	}
	if err := info.Validate(); err != nil {
		return err
	}
	if info.Pull && !slices.Contains(m.Capabilities, "pull") {
		return errors.New("pull is true, but the manifest doesn't declare the pull capability")
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
		if !inside(enc.Path, tmp) {
			return fmt.Errorf("encode %s: path %q is not directly inside blob_tmp (%s)", format, enc.Path, tmp)
		}
		if err := plainFile(enc.Path); err != nil {
			return fmt.Errorf("encode %s: %w", format, err)
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
		if dec.Avatar != "" && !inside(dec.Avatar, tmp) {
			return fmt.Errorf("decode %s: avatar %q is not directly inside blob_tmp (%s)", format, dec.Avatar, tmp)
		}
		if dec.Avatar != "" {
			if err := plainFile(dec.Avatar); err != nil {
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
