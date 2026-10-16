package protocol

import (
	"encoding/json"
	"regexp"
)

// The codec capability reads and writes card files. A codec lists the
// formats codec.encode writes in its manifest.
const (
	MethodCodecDecode = "codec.decode"
	MethodCodecEncode = "codec.encode"
)

// CodecDecodeParams names the file to read. Path is absolute; the plugin must
// not move or change the file.
type CodecDecodeParams struct {
	Path string `json:"path"`
}

// CodecDecodeResult is the card as CCv3, and its image: a file in blob_tmp,
// or empty.
type CodecDecodeResult struct {
	Card   json.RawMessage `json:"card"`
	Avatar string          `json:"avatar,omitempty"`
}

// CodecEncodeParams asks for a card file in Format, one of the manifest's
// formats. Avatar is the absolute path of the avatar file in a blob store, or
// empty.
type CodecEncodeParams struct {
	Card   json.RawMessage `json:"card"`
	Format string          `json:"format"`
	Avatar string          `json:"avatar,omitempty"`
}

// CodecEncodeResult names the file the plugin wrote directly inside
// blob_tmp, and its file name extension without the dot.
type CodecEncodeResult struct {
	Path      string `json:"path"`
	Extension string `json:"extension"`
}

var extensionPattern = regexp.MustCompile(`^[a-z0-9]{1,10}$`)

// ValidExtension reports whether ext is an extension a codec may return.
func ValidExtension(ext string) bool { return extensionPattern.MatchString(ext) }
