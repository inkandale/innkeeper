package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/inkandale/innkeeper/protocol"
)

// fakeInputs are the inputs any fake action reads. Call makes that
// library.call and puts the reply, or the error, in raw.
var fakeInputs = json.RawMessage(`{"type": "object", "properties": {
	"sleep_ms": {"type": "integer"}, "fail": {"type": "boolean"}, "call": {"type": "object"}}}`)

// fakeActions are a field action, a whole-card action, a report-only one, a
// new-card one, one that proposes changes outside its scope, one that reads
// the library, and a whole-lorebook and a lorebook field action.
func fakeActions(mode string) protocol.ActionListResult {
	if mode == "bad-actions" {
		return protocol.ActionListResult{Actions: []protocol.Action{{ID: "Bad ID", Label: "Bad", Applies: "card"}},
			Skills: []protocol.Skill{{Name: "Bad Skill"}}}
	}
	return protocol.ActionListResult{Actions: []protocol.Action{
		{ID: "tighten", Label: "Tighten", Description: "Shortens a field.", Applies: "card", Fields: []string{"description", "personality"}},
		{ID: "reforge", Label: "Reforge", Description: "Rewrites the card from an instruction.", Applies: "card", Instruction: true, Inputs: fakeInputs},
		{ID: "evaluate", Label: "Evaluate", Description: "Scores the card.", Applies: "card", ReportOnly: true},
		{ID: "generate", Label: "Generate", Description: "Makes a card from a concept.", Applies: "new", Instruction: true, Images: true},
		{ID: "sneaky", Label: "Sneaky", Description: "Changes more than its field.", Applies: "card", Fields: []string{"description"}},
		{ID: "lookup", Label: "Look up", Description: "Reads the library.", Applies: "card", Library: true, Inputs: fakeInputs},
		{ID: "lore", Label: "Rework lore", Description: "Reworks the entries from an instruction.", Applies: "lorebook", Instruction: true, Inputs: fakeInputs},
		{ID: "tighten-lore", Label: "Tighten", Description: "Shortens a field.", Applies: "lorebook", Fields: []string{"description"}},
	}, Skills: []protocol.Skill{{Name: "college", Description: "Campus life."}, {Name: "noir"}}}
}

func actionRun(ctx context.Context, conn *protocol.Conn, params json.RawMessage, config map[string]any, mode string) (any, error) {
	var p protocol.ActionRunParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, protocol.NewError(protocol.CodeInvalidParams, err.Error(), false)
	}
	var in struct {
		SleepMS int             `json:"sleep_ms"`
		Fail    bool            `json:"fail"`
		Call    json.RawMessage `json:"call"`
	}
	_ = json.Unmarshal(p.Inputs, &in)
	_ = conn.Notify(protocol.NotifyProgress, protocol.Progress{Run: p.Run, Message: "Running " + p.Action, Done: 1, Total: 2})
	time.Sleep(time.Duration(in.SleepMS) * time.Millisecond)
	if in.Fail {
		return nil, protocol.NewError(-32000, "the model refused", true)
	}
	if id, ok := config["connection"].(float64); ok {
		if err := chatWithTool(ctx, conn, p.Run, int64(id), mode); err != nil {
			return nil, err
		}
	}
	var res protocol.ActionRunResult
	if len(in.Call) > 0 {
		res.Raw = libraryCall(ctx, conn, p.Run, in.Call)
	}
	data := map[string]any{}
	_ = json.Unmarshal(p.Entity.Data, &data)
	switch p.Action {
	case "tighten", "tighten-lore":
		data[p.Field] = fmt.Sprintf("Tight: %v", data[p.Field])
	case "reforge":
		data["description"] = p.Instruction
		if res.Raw == "" {
			res.Raw = "skills: " + strings.Join(p.Context.Skills, ",")
		}
	case "evaluate":
		// Report-only, yet it proposes a rename the core must drop.
		data["name"] = "Renamed"
		score := 64.0
		res.Report = &protocol.Report{Summary: "Needs work.", Score: &score,
			Dimensions: []protocol.ReportDimension{{Key: "voice", Label: "Voice", Score: 80, Comment: "Distinct."}},
			Findings: []protocol.Finding{
				{Severity: "high", Text: "Too short.", Fields: []string{"description", "personality"}},
				{Severity: "medium", Text: "Belongs in the description.", Fields: []string{"personality"},
					Move: &protocol.FindingMove{Excerpt: "Gruff", To: "description"}}},
			Suggestions: []string{"Give the card a rival."}}
	case "generate":
		data = map[string]any{"name": "Generated", "description": p.Instruction}
		images, _ := json.Marshal(p.Images)
		res.Raw = string(images)
	case "sneaky":
		data[p.Field] = "Sneaky"
		data["name"] = "Renamed"
		data["character_book"] = map[string]any{"entries": []any{}}
	case "lore":
		data["entries"] = reworked(data["entries"], p.Instruction)
	case "lookup":
	default:
		return nil, protocol.NewError(protocol.CodeInvalidParams, "no action "+p.Action, false)
	}
	res.Proposal, _ = json.Marshal(data)
	return res, nil
}

// libraryCall sends call, a library.call's name and arguments, and returns
// the reply's text or the error.
func libraryCall(ctx context.Context, conn *protocol.Conn, run string, call json.RawMessage) string {
	var params protocol.LibraryCallParams
	_ = json.Unmarshal(call, &params)
	params.Run = run
	var out protocol.MCPCallResult
	err := conn.Call(ctx, protocol.MethodLibraryCall, params, &out)
	var rpcErr *protocol.Error
	if errors.As(err, &rpcErr) {
		return fmt.Sprintf("error %d: %s", rpcErr.Code, rpcErr.Message)
	}
	if err != nil {
		return "error: " + err.Error()
	}
	text, _ := json.Marshal(out)
	return string(text)
}

// reworked changes the first entry, removes the second, and adds one holding
// instruction, so a run proposes each kind of entry change.
func reworked(v any, instruction string) []any {
	entries, _ := v.([]any)
	var out []any
	for i, e := range entries {
		entry, ok := e.(map[string]any)
		switch {
		case i == 0 && ok:
			entry["content"] = fmt.Sprintf("Reworked: %v", entry["content"])
		case i == 1:
			continue
		}
		out = append(out, e)
	}
	return append(out, map[string]any{"keys": []any{"new"}, "content": instruction, "enabled": true,
		"insertion_order": 100, "extensions": map[string]any{}})
}
