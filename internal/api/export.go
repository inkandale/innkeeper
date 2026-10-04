package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"strings"

	"github.com/davidgibbons/innkeeper/internal/blobstore"
	"github.com/davidgibbons/innkeeper/protocol"
)

// errBadCodec means a codec plugin's reply broke the protocol.
var errBadCodec = errors.New("codec plugin sent a bad reply")

// encode writes a card in format with the oldest working codec instance that
// lists it. revID is the revision whose avatar goes in the file.
func (s *Server) encode(ctx context.Context, revID int64, card json.RawMessage, format string) ([]byte, string, error) {
	inst, err := s.Registry.CodecInstance(ctx, format)
	if err != nil {
		return nil, "", err
	}
	rev, err := s.Lib.Cards.Revision(ctx, revID)
	if err != nil {
		return nil, "", err
	}
	params := protocol.CodecEncodeParams{Card: card, Format: format}
	if rev.AvatarBlob != nil {
		if params.Avatar, err = s.Lib.BlobPath(*rev.AvatarBlob); err != nil {
			return nil, "", err
		}
	}
	var res protocol.CodecEncodeResult
	if err := s.Registry.Call(ctx, s.Plugins, inst, protocol.MethodCodecEncode, params, &res); err != nil {
		return nil, "", err
	}
	if !protocol.ValidExtension(res.Extension) {
		return nil, "", fmt.Errorf("%w: extension %q", errBadCodec, res.Extension)
	}
	// IngestBlob refuses a path outside blob_tmp. Nothing references the
	// blob, so cleanup removes it after a day.
	b, err := s.Lib.IngestBlob(ctx, res.Path)
	if errors.Is(err, blobstore.ErrInvalid) {
		s.Log.Error("codec wrote a bad file", "format", format, "err", err)
		return nil, "", fmt.Errorf("%w: it named a file the server can't accept", errBadCodec)
	}
	if err != nil {
		return nil, "", err
	}
	// ponytail: checked after IngestBlob stored the file, so a runaway codec
	// can fill the disk until cleanup; check the size before ingesting if
	// that matters.
	if b.Size > maxBlobBytes {
		return nil, "", fmt.Errorf("%w: file is over %d MiB", errBadCodec, maxBlobBytes>>20)
	}
	_, f, err := s.Lib.OpenBlob(ctx, b.SHA256)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	body, err := io.ReadAll(f)
	return body, res.Extension, err
}

// exportType is an export's Content-Type. Only JSON and images other than
// SVG keep their type, so an export can't run as a page on this site.
func exportType(ext string) string {
	t := mime.TypeByExtension("." + ext)
	if ext == "json" || strings.HasPrefix(t, "image/") && !strings.HasPrefix(t, "image/svg") {
		return t
	}
	return "application/octet-stream"
}
