package contract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/protocol"
)

const (
	checkActionListName = "action.list returns valid actions and skills"
	checkActionRunsName = "action.run of each action returns a valid reply or a plugin error"
)

// checkActions lists the plugin's actions, then runs each on a sample entity.
func checkActions(ctx context.Context, p *pluginhost.Process) []Result {
	var res protocol.ActionListResult
	err := call(ctx, p, protocol.MethodActionList, nil, &res)
	if err == nil {
		seen := map[string]bool{}
		var errs []error
		for _, a := range res.Actions {
			if seen[a.ID] {
				errs = append(errs, fmt.Errorf("action id %q repeats", a.ID))
			}
			seen[a.ID] = true
			errs = append(errs, a.Validate())
		}
		seen = map[string]bool{}
		for _, sk := range res.Skills {
			if seen[sk.Name] {
				errs = append(errs, fmt.Errorf("skill %q repeats", sk.Name))
			}
			seen[sk.Name] = true
			errs = append(errs, sk.Validate())
		}
		err = errors.Join(errs...)
	}
	results := []Result{{checkActionListName, err}}
	if err != nil || dead(err, p) {
		return results
	}
	return append(results, Result{checkActionRunsName, checkActionRuns(ctx, p, res.Actions)})
}

var sampleLorebook = json.RawMessage(`{"name": "Contract Check", "entries": []}`)

// checkActionRuns runs each action with its first field and no inputs. A
// plugin checked without its config, such as an AI without a model, may only
// fail, so an error other than -32601 counts as an answer.
func checkActionRuns(ctx context.Context, p *pluginhost.Process, list []protocol.Action) error {
	var card struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(sampleCard, &card); err != nil {
		return err
	}
	for _, a := range list {
		params := protocol.ActionRunParams{Run: "contract-check", Action: a.ID, Inputs: json.RawMessage(`{}`),
			Entity:  protocol.ActionEntity{Kind: protocol.ApplyCard, Data: card.Data},
			Context: protocol.ActionContext{Cards: []protocol.ContextItem{}, Lorebooks: []protocol.ContextItem{}, Skills: []string{}}}
		switch a.Applies {
		case protocol.ApplyNew:
			params.Entity.Data = nil
		case protocol.ApplyLorebook:
			params.Entity = protocol.ActionEntity{Kind: protocol.ApplyLorebook, Data: sampleLorebook}
		}
		if len(a.Fields) > 0 {
			params.Field = a.Fields[0]
		}
		var res protocol.ActionRunResult
		err := call(ctx, p, protocol.MethodActionRun, params, &res)
		var rpcErr *protocol.Error
		switch {
		case errors.As(err, &rpcErr) && rpcErr.Code != protocol.CodeMethodNotFound:
			continue
		case err != nil:
			return fmt.Errorf("%s: %w", a.ID, err)
		case res.Report != nil:
			if err := res.Report.Validate(); err != nil {
				return fmt.Errorf("%s: %w", a.ID, err)
			}
		}
		var proposal map[string]json.RawMessage
		if len(res.Proposal) > 0 && (json.Unmarshal(res.Proposal, &proposal) != nil || proposal == nil) {
			return fmt.Errorf("%s: proposal is not a JSON object", a.ID)
		}
	}
	return nil
}
