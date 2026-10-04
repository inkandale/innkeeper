package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/davidgibbons/innkeeper/protocol"
)

// The fake codec's one format, "fake", is "fake:" followed by the card JSON.
// bad makes it write outside blob_tmp and accept any file as a card.
func codecEncode(blobTmp string, params json.RawMessage, bad bool) (any, error) {
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
	dir := blobTmp
	// The folder holding blob_tmp is outside it. A card named Escape tests
	// that the core refuses such paths.
	if bad || c.Data.Name == "Escape" {
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
	return protocol.CodecEncodeResult{Path: f.Name(), Extension: "txt"}, nil
}

func codecDecode(params json.RawMessage, bad bool) (any, error) {
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
		if !bad {
			return nil, protocol.NewError(protocol.CodeInvalidParams, "not a fake card", false)
		}
		card = []byte(`{"spec": "chara_card_v3", "spec_version": "3.0", "data": {"name": "Anything"}}`)
	}
	return protocol.CodecDecodeResult{Card: card}, nil
}
