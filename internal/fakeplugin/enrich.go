package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/davidgibbons/innkeeper/protocol"
)

// enrichRun writes the keys testutil.FakeEnrichments declares.
// quality is the description's length, at most 100. A card whose name holds
// "fail" gets an error, "skip" is left out of the reply, "bad" gets an
// undeclared key, a string voice, and quality 150, "nul" gets a NUL in its
// summary and meta, and "slow" takes a second.
func enrichRun(ctx context.Context, conn *protocol.Conn, params json.RawMessage, mode string) (any, error) {
	var p protocol.EnrichRunParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, protocol.NewError(protocol.CodeInvalidParams, err.Error(), false)
	}
	_ = conn.Notify(protocol.NotifyProgress, protocol.Progress{Run: p.Run, Message: fmt.Sprintf("Evaluating %d cards", len(p.Revisions)), Total: int64(len(p.Revisions))})
	res := protocol.EnrichRunResult{Results: []protocol.EnrichResult{}}
	for _, rev := range p.Revisions {
		var data struct{ Name, Description string }
		_ = json.Unmarshal(rev.Data, &data)
		name := strings.ToLower(data.Name)
		r := protocol.EnrichResult{RevisionID: rev.RevisionID, Meta: json.RawMessage(`{"model": "fake-1"}`)}
		quality := min(len(data.Description), 100)
		r.Values = map[string]json.RawMessage{
			"quality": json.RawMessage(fmt.Sprint(quality)),
			"voice":   json.RawMessage("50"),
			"summary": json.RawMessage(fmt.Sprintf("%q", "About "+data.Name)),
		}
		switch {
		case mode == "bad-enrich":
			r.Values = map[string]json.RawMessage{"mood": json.RawMessage(`"grim"`)}
		case strings.Contains(name, "slow"):
			select {
			case <-time.After(time.Second):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		case strings.Contains(name, "fail"):
			r = protocol.EnrichResult{RevisionID: rev.RevisionID, Error: "the model returned no JSON"}
		case strings.Contains(name, "skip"):
			continue
		case strings.Contains(name, "nul"):
			r.Values["summary"] = json.RawMessage(`"a\u0000b"`)
			r.Meta = json.RawMessage(`{"model": "\u0000"}`)
		case strings.Contains(name, "bad"):
			r.Values = map[string]json.RawMessage{"mood": json.RawMessage(`"grim"`), "voice": json.RawMessage(`"loud"`),
				"quality": json.RawMessage("150"), "summary": json.RawMessage(`"Bad."`)}
		}
		res.Results = append(res.Results, r)
	}
	return res, nil
}
