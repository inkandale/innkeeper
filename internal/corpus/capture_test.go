// Package corpus_test captures how the codec, SillyTavern and Lumiverse read
// each card in testdata/corpus, as golden files beside the card.
package corpus_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/inkandale/innkeeper/cardhash"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/protocol"
)

const corpusDir = "../../testdata/corpus"

// The servers testdata/corpus/compose.yaml runs.
var servers = []struct {
	name    string
	config  map[string]any
	secrets map[string]string
}{
	{"sillytavern", map[string]any{"url": "http://localhost:18001", "auth": "none"}, nil},
	{"lumiverse", map[string]any{"url": "http://localhost:18002", "username": "corpus"},
		map[string]string{"password": "corpus-throwaway-password"}},
}

// TestCapture rewrites the golden files. It needs the compose servers up and
// INNKEEPER_PLUGINS_DIR holding the installed plugins.
func TestCapture(t *testing.T) {
	if os.Getenv("INNKEEPER_CORPUS_CAPTURE") != "1" {
		t.Skip("set INNKEEPER_CORPUS_CAPTURE=1 to capture the corpus")
	}
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	dir := os.Getenv("INNKEEPER_PLUGINS_DIR")
	if dir == "" {
		t.Fatal("INNKEEPER_PLUGINS_DIR is not set")
	}
	manifests, err := plugins.LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	tmp := filepath.Join(root, "tmp")
	if err := os.Mkdir(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	start := func(name string, config map[string]any, secrets map[string]string) *pluginhost.Process {
		m := manifests[name]
		if m == nil {
			t.Fatalf("plugin %s is not in INNKEEPER_PLUGINS_DIR", name)
		}
		p, err := pluginhost.Start(ctx, m, protocol.InitializeParams{InstanceID: "corpus-" + name, Config: config,
			Secrets: secrets, BlobStores: map[string]string{"corpus": root}, BlobTmp: tmp}, noCore, log)
		if err != nil {
			t.Fatalf("start %s: %v", name, err)
		}
		t.Cleanup(func() { _ = p.Stop() })
		return p
	}
	codec := start("card-codec", map[string]any{}, nil)
	procs := map[string]*pluginhost.Process{}
	for _, tg := range servers {
		procs[tg.name] = start(tg.name, tg.config, tg.secrets)
		// The capture writes into the app, so refuse any server but a fresh one.
		for _, m := range []string{protocol.MethodTargetList, protocol.MethodTargetListLorebooks} {
			var list protocol.TargetListResult
			if err := call(ctx, procs[tg.name], m, nil, &list); err != nil {
				t.Fatalf("%s %s: %v", tg.name, m, err)
			}
			if len(list.Items) > 0 {
				t.Fatalf("%s already has %d items from %s; capture only into the fresh compose servers", tg.name, len(list.Items), m)
			}
		}
	}

	sources, err := filepath.Glob(filepath.Join(corpusDir, "*", "source.*"))
	if err != nil || len(sources) == 0 {
		t.Fatalf("no corpus sources: %v", err)
	}
	for _, src := range sources {
		dir := filepath.Dir(src)
		slug := filepath.Base(dir)
		for _, f := range []string{"codec.json", "codec.error", "sillytavern.json", "sillytavern.error", "lumiverse.json", "lumiverse.error"} {
			_ = os.Remove(filepath.Join(dir, f))
		}
		abs, err := filepath.Abs(src)
		if err != nil {
			t.Fatal(err)
		}
		var dec protocol.CodecDecodeResult
		err = call(ctx, codec, protocol.MethodCodecDecode, protocol.CodecDecodeParams{Path: abs}, &dec)
		var data, book json.RawMessage
		if err == nil {
			_, data, book, err = library.SplitCard(dec.Card)
		}
		if err != nil {
			writeError(t, filepath.Join(dir, "codec.error"), err)
			continue
		}
		// The core keeps the avatar in its blob store under its hash, and the
		// plugins remember that file name.
		avatar, avatarSHA, avatarPhash := "", "", ""
		if dec.Avatar != "" {
			if avatarSHA, avatarPhash, err = imageHashes(dec.Avatar); err != nil {
				t.Fatal(err)
			}
			avatar = filepath.Join(root, avatarSHA[0:2], avatarSHA[2:4], avatarSHA)
			if err := os.MkdirAll(filepath.Dir(avatar), 0o700); err != nil {
				t.Fatal(err)
			}
			// The store root and its tmp folder are both under root, so this
			// never crosses file systems.
			if err := os.Rename(dec.Avatar, avatar); err != nil {
				t.Fatal(err)
			}
		}
		writeJSON(t, filepath.Join(dir, "codec.json"), map[string]any{"card": dec.Card, "lorebook": book, "avatar_sha256": avatarSHA,
			"avatar_phash": avatarPhash}, nil)

		card, err := json.Marshal(map[string]any{"spec": "chara_card_v3", "spec_version": "3.0", "data": data})
		if err != nil {
			t.Fatal(err)
		}
		for _, tg := range servers {
			out, ids, err := capture(ctx, procs[tg.name], slug, card, book, avatar)
			if err != nil {
				writeError(t, filepath.Join(dir, tg.name+".error"), err)
				t.Logf("%s %s: %v", slug, tg.name, err)
				continue
			}
			writeJSON(t, filepath.Join(dir, tg.name+".json"), out, ids)
		}
	}
}

// capture pushes one card, and its lorebook if any, then reads both back. ids
// maps the remote IDs it saw to placeholders.
func capture(ctx context.Context, p *pluginhost.Process, slug string, card, book json.RawMessage, avatar string) (map[string]any, map[string]string, error) {
	var info protocol.TargetInfo
	if err := call(ctx, p, protocol.MethodTargetDescribe, nil, &info); err != nil {
		return nil, nil, fmt.Errorf("describe: %w", err)
	}
	ids := map[string]string{}
	lorebookIDs := []string{}
	if book != nil {
		var put protocol.TargetPutResult
		if err := call(ctx, p, protocol.MethodTargetPutLorebook,
			protocol.TargetPutLorebookParams{Lorebook: book, Key: "corpus:" + slug + ":lorebook"}, &put); err != nil {
			return nil, nil, fmt.Errorf("put_lorebook: %w", err)
		}
		ids[put.RemoteID] = "<lorebook-id-0>"
		lorebookIDs = append(lorebookIDs, put.RemoteID)
	}
	var put protocol.TargetPutResult
	if err := call(ctx, p, protocol.MethodTargetPut, protocol.TargetPutParams{Card: card, Avatar: avatar,
		Key: "corpus:" + slug, LorebookRemoteIDs: lorebookIDs}, &put); err != nil {
		return nil, nil, fmt.Errorf("put: %w", err)
	}
	ids[put.RemoteID] = "<card-id>"
	var got protocol.TargetGetResult
	if err := call(ctx, p, protocol.MethodTargetGet, protocol.TargetGetParams{RemoteID: put.RemoteID, Avatar: true}, &got); err != nil {
		return nil, nil, fmt.Errorf("get: %w", err)
	}
	avatarSHA, avatarPhash := "", ""
	if got.Avatar != "" {
		var err error
		avatarSHA, avatarPhash, err = imageHashes(got.Avatar)
		_ = os.Remove(got.Avatar)
		if err != nil {
			return nil, nil, err
		}
	}
	lorebooks := []json.RawMessage{}
	for i, id := range got.LorebookRemoteIDs {
		if _, ok := ids[id]; !ok {
			ids[id] = fmt.Sprintf("<lorebook-id-%d>", i)
		}
		var lb protocol.TargetGetLorebookResult
		if err := call(ctx, p, protocol.MethodTargetGetLorebook, protocol.TargetGetParams{RemoteID: id}, &lb); err != nil {
			return nil, nil, fmt.Errorf("get_lorebook: %w", err)
		}
		lorebooks = append(lorebooks, lb.Lorebook)
	}
	return map[string]any{"fields": info.Fields, "lorebook_fields": info.LorebookFields, "card": got.Card, "lorebook_remote_ids": got.LorebookRemoteIDs,
		"lorebooks": lorebooks, "avatar_sha256": avatarSHA, "avatar_phash": avatarPhash}, ids, nil
}

func call(ctx context.Context, p *pluginhost.Process, method string, params, result any) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	return p.Call(ctx, method, params, result)
}

func noCore(_ context.Context, method string, _ json.RawMessage) (any, error) {
	return nil, protocol.MethodNotFound(method)
}

// imageHashes returns an image's SHA-256 and its perceptual hash in hex.
// SillyTavern re-encodes avatars, so the corpus test compares the phash.
func imageHashes(path string) (sha, phash string, err error) {
	if sha, err = fileSHA(path); err != nil {
		return "", "", err
	}
	p, err := cardhash.ImagePhash(path)
	if err != nil {
		return "", "", fmt.Errorf("phash %s: %w", path, err)
	}
	return sha, fmt.Sprintf("%016x", uint64(p)), nil
}

// fileSHA hashes a file. A PNG's text and time chunks are left out:
// SillyTavern writes the card and a timestamp into them, so only the image
// itself is compared.
func fileSHA(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	const sig = "\x89PNG\r\n\x1a\n"
	if !strings.HasPrefix(string(b), sig) {
		h.Write(b)
		return hex.EncodeToString(h.Sum(nil)), nil
	}
	h.Write(b[:len(sig)])
	// Each chunk is a 4-byte length, a 4-byte type, the data, and a 4-byte CRC.
	for rest := b[len(sig):]; len(rest) >= 12; {
		n := 12 + int(binary.BigEndian.Uint32(rest))
		if n > len(rest) {
			n = len(rest)
		}
		switch string(rest[4:8]) {
		case "tEXt", "zTXt", "iTXt", "tIME":
		default:
			h.Write(rest[:n])
		}
		rest = rest[n:]
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

var uuidPattern = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

// writeJSON writes v with sorted keys and two-space indents. The UUID remote
// IDs in ids become placeholders, since the app makes new ones every run.
func writeJSON(t *testing.T, path string, v any, ids map[string]string) {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var tree any
	if err := dec.Decode(&tree); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(tree); err != nil {
		t.Fatal(err)
	}
	s := buf.String()
	for id, placeholder := range ids {
		if uuidPattern.MatchString(id) {
			s = strings.ReplaceAll(s, id, placeholder)
		}
	}
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeError(t *testing.T, path string, err error) {
	t.Helper()
	if err := os.WriteFile(path, []byte(err.Error()+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
