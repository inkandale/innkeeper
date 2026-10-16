package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/inkandale/innkeeper/protocol"
)

// The fake codec's one format, "fake", is "fake:" followed by the card JSON.
// bad-codec makes it write outside blob_tmp and accept any file as a card.
// The other codec- modes each break one rule the contract kit checks.
func codecEncode(blobTmp string, params json.RawMessage, mode string) (any, error) {
	var p protocol.CodecEncodeParams
	if err := json.Unmarshal(params, &p); err != nil || p.Format != "fake" {
		return nil, protocol.NewError(protocol.CodeInvalidParams, "want format fake", false)
	}
	var c struct {
		Data struct {
			Name string `json:"name"`
		} `json:"data"`
	}
	_ = json.Unmarshal(p.Card, &c)
	if mode == "codec-no-avatar" && p.Avatar != "" {
		return nil, protocol.NewError(protocol.CodeInternalError, "can't embed an avatar", false)
	}
	dir := blobTmp
	// The folder holding blob_tmp is outside it. A card named Escape tests
	// that the core refuses such paths.
	if mode == "bad-codec" || c.Data.Name == "Escape" {
		dir = filepath.Dir(blobTmp)
	}
	f, err := os.CreateTemp(dir, "fake-*.txt")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := f.Write(append([]byte("fake:"), p.Card...)); err != nil {
		return nil, err
	}
	if mode == "codec-hard-link" {
		if err := os.Link(f.Name(), f.Name()+".link"); err != nil {
			return nil, err
		}
	}
	// A card named Mislabel tests that the core types the file by its
	// content, not the extension the codec claims.
	ext := "txt"
	if c.Data.Name == "Mislabel" {
		ext = "png"
	}
	return protocol.CodecEncodeResult{Path: f.Name(), Extension: ext}, nil
}

func codecDecode(params json.RawMessage, mode string) (any, error) {
	var p protocol.CodecDecodeParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, protocol.NewError(protocol.CodeInvalidParams, err.Error(), false)
	}
	b, err := os.ReadFile(p.Path)
	if err != nil {
		return nil, protocol.NewError(protocol.CodeInvalidParams, err.Error(), false)
	}
	card, ok := bytes.CutPrefix(b, []byte("fake:"))
	if !ok {
		if mode != "bad-codec" {
			code := protocol.CodeInvalidParams
			if mode == "codec-wrong-code" {
				code = protocol.CodeInternalError
			}
			return nil, protocol.NewError(code, "not a fake card", false)
		}
		card = []byte(`{"spec": "chara_card_v3", "spec_version": "3.0", "data": {"name": "Anything"}}`)
	}
	return protocol.CodecDecodeResult{Card: card}, nil
}
