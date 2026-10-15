package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync"

	"github.com/danielgtaylor/huma/v2"

	"github.com/davidgibbons/innkeeper/internal/actions"
	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/plugins"
	"github.com/davidgibbons/innkeeper/internal/tier"
	"github.com/davidgibbons/innkeeper/protocol"
)

// actionList lists an instance's actions.
var actionList = listMethod[protocol.ActionListResult, protocol.Action]{name: protocol.MethodActionList,
	items: func(r protocol.ActionListResult) []protocol.Action { return r.Actions },
	key:   func(a protocol.Action) string { return a.ID }, validate: protocol.Action.Validate}

// PluginAction is an action a plugin instance offers.
type PluginAction struct {
	protocol.Action
	InstanceID   int64  `json:"instance_id"`
	InstanceName string `json:"instance_name"`
}

type listActionsIn struct {
	Applies string `query:"applies" enum:"card,lorebook,new" doc:"Only actions that apply to this"`
}

type actionsOut struct{ Body []PluginAction }

type startActionIn struct {
	Body struct {
		InstanceID int64  `json:"instance_id"`
		Action     string `json:"action"`
		Field      string `json:"field,omitempty" doc:"For a field action: the data key it runs on"`
		Entity     struct {
			Kind string          `json:"kind" enum:"card,lorebook"`
			ID   int64           `json:"id,omitempty" doc:"The card or lorebook the draft belongs to; omit for one not saved yet"`
			Data json.RawMessage `json:"data,omitempty" doc:"The draft's data. Omit for a new card's action."`
		} `json:"entity"`
		Instruction string              `json:"instruction,omitempty" maxLength:"20000"`
		Inputs      map[string]any      `json:"inputs,omitempty" doc:"Values for the action's inputs schema"`
		Context     actions.ContextRefs `json:"context,omitempty"`
		Images      actions.ImageRefs   `json:"images,omitempty"`
	}
}

func (s *Server) actionRoutes(api huma.API) {
	tags := []string{"Actions"}
	huma.Register(api, huma.Operation{OperationID: "list-actions", Method: http.MethodGet, Path: "/actions",
		Summary: "List the actions plugin instances offer", Tags: tags,
		Description: "Instances that are broken, or whose plugin doesn't answer, are left out."},
		func(ctx context.Context, in *listActionsIn) (*actionsOut, error) {
			insts, err := s.Registry.InstancesWith(ctx, "action")
			if err != nil {
				return nil, s.fail(err)
			}
			var mu sync.Mutex
			out := []PluginAction{}
			eachWorking(insts, func(i plugins.InstanceInfo) {
				_, list, err := instanceList(ctx, s, &s.actions, actionList, i.ID)
				if err != nil {
					s.Log.Warn("list plugin actions", "instance", i.ID, "err", err)
					return
				}
				mu.Lock()
				defer mu.Unlock()
				for _, a := range list {
					if in.Applies == "" || a.Applies == in.Applies {
						out = append(out, PluginAction{a, i.ID, i.Name})
					}
				}
			})
			mu.Lock()
			defer mu.Unlock()
			out = slices.Clone(out)
			slices.SortStableFunc(out, func(a, b PluginAction) int { return int(a.InstanceID - b.InstanceID) })
			return &actionsOut{Body: out}, nil
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Write), OperationID: "start-action-run", Method: http.MethodPost,
		Path: "/actions/runs", Summary: "Run a plugin action", Tags: tags, DefaultStatus: http.StatusAccepted,
		Description: "Queues an action job. Its progress has the plugin's message, done, and total, and when the run ends, " +
			"result: the proposal cut to the action's scope, the report, raw, and warnings for each change dropped. " +
			"Cancel the job to stop the run."},
		func(ctx context.Context, in *startActionIn) (*jobResp, error) {
			b := in.Body
			if err := s.usable(ctx, b.InstanceID, "action"); err != nil {
				return nil, err
			}
			_, list, err := instanceList(ctx, s, &s.actions, actionList, b.InstanceID)
			if errors.Is(err, errListing) {
				return nil, huma.Error503ServiceUnavailable("the plugin is busy listing its actions; try again shortly")
			}
			if err != nil {
				return nil, s.fail(err)
			}
			i := slices.IndexFunc(list, func(a protocol.Action) bool { return a.ID == b.Action })
			if i < 0 {
				return nil, huma.Error400BadRequest(fmt.Sprintf("instance %d has no action %q", b.InstanceID, b.Action))
			}
			p := actions.Payload{Action: b.Action, Field: b.Field, Entity: protocol.ActionEntity{Kind: b.Entity.Kind, Data: b.Entity.Data},
				Instruction: b.Instruction, Context: b.Context, Images: b.Images}
			if b.Entity.Kind == protocol.ApplyLorebook {
				p.LorebookID = b.Entity.ID
			} else {
				p.CardID = b.Entity.ID
			}
			if b.Inputs != nil {
				if p.Inputs, err = json.Marshal(b.Inputs); err != nil {
					return nil, err
				}
			}
			if err := p.Check(list[i]); err != nil {
				return nil, s.fail(err)
			}
			for _, sha := range []string{b.Images.Reference, b.Images.Avatar} {
				if sha == "" {
					continue
				}
				if _, err := s.Lib.Blob(ctx, sha); err != nil {
					return nil, s.fail(err)
				}
			}
			return s.enqueue(ctx, jobs.Spec{Kind: actions.Kind, InstanceID: &b.InstanceID, Payload: p})
		})
}
