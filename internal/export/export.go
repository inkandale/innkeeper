// Package export writes library cards as files.
package export

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/davidgibbons/innkeeper/internal/blobstore"
	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/internal/plugins"
	"github.com/davidgibbons/innkeeper/protocol"
)

// ErrBadCodec means a codec plugin's reply broke the protocol.
var ErrBadCodec = errors.New("codec plugin sent a bad reply")

// Exporter writes cards as JSON, or in a format a codec plugin writes.
type Exporter struct {
	Lib      *library.Library
	Registry *plugins.Registry
	Plugins  *pluginhost.Manager
	Log      *slog.Logger
}

// File is one exported card.
type File struct {
	Name string // slug and extension, such as brakka.png
	Type string // the type sniffed when the file was stored
	Body []byte
}

// Card exports a card's head with its linked lorebooks embedded. format is
// json, or a format a codec plugin instance writes.
func (e *Exporter) Card(ctx context.Context, id int64, format string) (File, error) {
	c, err := e.Lib.Cards.Get(ctx, id)
	if err != nil {
		return File{}, err
	}
	raw, err := e.Lib.ExportCard(ctx, id)
	if err != nil {
		return File{}, err
	}
	if format == "json" {
		return File{Name: c.Slug + ".json", Type: "application/json", Body: raw}, nil
	}
	body, ext, typ, err := e.encode(ctx, c.HeadRevisionID, raw, format)
	if err != nil {
		return File{}, err
	}
	return File{Name: c.Slug + "." + ext, Type: typ, Body: body}, nil
}

// encode writes a card in format with the oldest working codec instance that
// lists it. revID is the revision whose avatar goes in the file. It returns
// the file, its extension and the type sniffed when it was stored.
func (e *Exporter) encode(ctx context.Context, revID int64, card json.RawMessage, format string) ([]byte, string, string, error) {
	inst, err := e.Registry.CodecInstance(ctx, format)
	if err != nil {
		return nil, "", "", err
	}
	rev, err := e.Lib.Cards.Revision(ctx, revID)
	if err != nil {
		return nil, "", "", err
	}
	params := protocol.CodecEncodeParams{Card: card, Format: format}
	if rev.AvatarBlob != nil {
		if params.Avatar, err = e.Lib.BlobPath(*rev.AvatarBlob); err != nil {
			return nil, "", "", err
		}
	}
	var res protocol.CodecEncodeResult
	if err := e.Registry.Call(ctx, e.Plugins, inst, protocol.MethodCodecEncode, params, &res); err != nil {
		return nil, "", "", err
	}
	if !protocol.ValidExtension(res.Extension) {
		return nil, "", "", fmt.Errorf("%w: extension %q", ErrBadCodec, res.Extension)
	}
	// IngestBlob refuses a path outside blob_tmp or a file over 32 MiB.
	// Nothing references the blob, so cleanup removes it after a day.
	b, err := e.Lib.IngestBlob(ctx, res.Path)
	if errors.Is(err, blobstore.ErrInvalid) {
		e.Log.Error("codec wrote a bad file", "format", format, "err", err)
		return nil, "", "", fmt.Errorf("%w: it named a file the server can't accept", ErrBadCodec)
	}
	if err != nil {
		return nil, "", "", err
	}
	_, f, err := e.Lib.OpenBlob(ctx, b.SHA256)
	if err != nil {
		return nil, "", "", err
	}
	defer f.Close()
	body, err := io.ReadAll(f)
	return body, res.Extension, b.MIME, err
}
