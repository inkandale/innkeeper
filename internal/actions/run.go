package actions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"sync"

	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/mcp"
	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/internal/plugins"
	"github.com/davidgibbons/innkeeper/internal/tier"
	"github.com/davidgibbons/innkeeper/protocol"
)

// libraryTools are the MCP tools library.call serves: the read tools that
// read the library, not apps or settings.
var libraryTools = []string{"search_cards", "get_card", "get_lorebook", "get_history"}

// Runner runs action jobs.
type Runner struct {
	Lib      *library.Library
	Registry *plugins.Registry
	// Plugins runs the action plugins. Its core handler must be Runs.Handle.
	Plugins *pluginhost.Manager
	Runs    *pluginhost.Runs
	// API serves library.call's tools in process; see mcp.Call.
	API http.Handler
	Log *slog.Logger
}

// Run is the jobs.Handler for Kind. Every failure is final: a run costs the
// owner money, so running it again is the owner's choice.
func (rn *Runner) Run(ctx context.Context, r *jobs.Run) error {
	if err := rn.run(ctx, r); err != nil {
		return fmt.Errorf("%w: %w", jobs.ErrFinal, err)
	}
	return nil
}

func (rn *Runner) run(ctx context.Context, r *jobs.Run) error {
	// A worker that died mid-run leaves the job to be claimed again.
	if r.Attempts > 1 {
		return errors.New("the run was interrupted; start it again")
	}
	if r.InstanceID == nil {
		return errors.New("action job has no plugin instance")
	}
	var p Payload
	if err := json.Unmarshal(r.Payload, &p); err != nil {
		return fmt.Errorf("action payload: %w", err)
	}
	inst, err := rn.Registry.InstanceWith(ctx, *r.InstanceID, "action")
	if err != nil {
		return err
	}
	a, err := rn.declaration(ctx, inst, p.Action)
	if err != nil {
		return err
	}
	if err := p.Check(a); err != nil {
		return err
	}
	params, err := rn.resolve(ctx, p)
	if err != nil {
		return err
	}
	params.Run = r.Token()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s := &session{rn: rn, ctx: ctx, run: r, action: a, instanceID: *r.InstanceID}
	defer rn.Runs.Add(params.Run, s.serve)()

	var res protocol.ActionRunResult
	if err := rn.Registry.Call(ctx, rn.Plugins, inst, protocol.MethodActionRun, params, &res); err != nil {
		return err
	}
	if res.Report != nil {
		if err := res.Report.Validate(); err != nil {
			return fmt.Errorf("plugin sent an invalid report: %w", err)
		}
	}
	proposal, warnings, err := Scope(a, p.Field, p.Entity.Kind, p.Entity.Data, res.Proposal)
	if err != nil {
		return fmt.Errorf("plugin sent an invalid proposal: %w", err)
	}
	s.mu.Lock()
	s.p.Result = &Result{Proposal: proposal, Report: res.Report, Raw: res.Raw, Warnings: orEmpty(warnings)}
	s.mu.Unlock()
	s.publish()
	return nil
}

// declaration is the action id that inst's plugin offers now.
func (rn *Runner) declaration(ctx context.Context, inst pluginhost.Instance, id string) (protocol.Action, error) {
	var list protocol.ActionListResult
	if err := rn.Registry.Call(ctx, rn.Plugins, inst, protocol.MethodActionList, nil, &list); err != nil {
		return protocol.Action{}, err
	}
	for _, a := range list.Actions {
		if a.ID == id && a.Validate() == nil {
			return a, nil
		}
	}
	return protocol.Action{}, fmt.Errorf("%w: the plugin no longer offers action %q", ErrInvalid, id)
}

// resolve turns the payload into action.run's params: context IDs into head
// revisions, and image SHA-256s into blob paths.
func (rn *Runner) resolve(ctx context.Context, p Payload) (protocol.ActionRunParams, error) {
	out := protocol.ActionRunParams{Action: p.Action, Field: p.Field, Entity: p.Entity, Instruction: p.Instruction,
		Inputs: p.Inputs, Context: protocol.ActionContext{Skills: orEmpty(p.Context.Skills)}}
	if len(out.Inputs) == 0 {
		out.Inputs = json.RawMessage(`{}`)
	}
	var err error
	if out.Context.Cards, err = rn.heads(ctx, library.Card, p.Context.Cards); err != nil {
		return out, err
	}
	if out.Context.Lorebooks, err = rn.heads(ctx, library.Lorebook, p.Context.Lorebooks); err != nil {
		return out, err
	}
	if out.Images.Reference, err = rn.blobPath(ctx, p.Images.Reference); err != nil {
		return out, err
	}
	out.Images.Avatar, err = rn.blobPath(ctx, p.Images.Avatar)
	return out, err
}

func (rn *Runner) heads(ctx context.Context, k *library.Kind, ids []int64) ([]protocol.ContextItem, error) {
	ents := rn.Lib.Entities(k)
	out := []protocol.ContextItem{}
	for _, id := range ids {
		e, err := ents.Get(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("context %s %d: %w", k.Name, id, err)
		}
		rev, err := ents.Revision(ctx, e.HeadRevisionID)
		if err != nil {
			return nil, fmt.Errorf("context %s %d: %w", k.Name, id, err)
		}
		out = append(out, protocol.ContextItem{ID: id, Name: library.DataName(rev.Data), Data: rev.Data})
	}
	return out, nil
}

// blobPath is where a stored blob's file is, or "" for no blob.
func (rn *Runner) blobPath(ctx context.Context, sha string) (string, error) {
	if sha == "" {
		return "", nil
	}
	if _, err := rn.Lib.Blob(ctx, sha); err != nil {
		return "", fmt.Errorf("image %s: %w", sha, err)
	}
	return rn.Lib.BlobPath(sha)
}

// session is one running action.run call.
type session struct {
	rn         *Runner
	ctx        context.Context
	run        *jobs.Run
	action     protocol.Action
	instanceID int64

	mu sync.Mutex
	p  Progress
}

// serve answers the plugin's calls for this run.
func (s *session) serve(_ context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case protocol.NotifyProgress:
		var p protocol.Progress
		if json.Unmarshal(params, &p) == nil {
			s.mu.Lock()
			s.p.Message, s.p.Done, s.p.Total = p.Message, p.Done, p.Total
			s.mu.Unlock()
			s.publish()
		}
		return nil, nil
	case protocol.MethodLibraryCall:
		return s.libraryCall(params)
	}
	return nil, protocol.MethodNotFound(method)
}

func (s *session) publish() {
	s.mu.Lock()
	raw, _ := json.Marshal(s.p)
	s.mu.Unlock()
	s.run.SetProgress(raw)
}

// libraryCall runs a library tool for the plugin, if its action declared
// library and the owner turned on Library access for the instance.
func (s *session) libraryCall(params json.RawMessage) (any, error) {
	var c protocol.LibraryCallParams
	if err := json.Unmarshal(params, &c); err != nil {
		return nil, protocol.NewError(protocol.CodeInvalidParams, err.Error(), false)
	}
	if !s.action.Library {
		return nil, forbidden(fmt.Sprintf("action %s doesn't declare library", s.action.ID))
	}
	if !slices.Contains(libraryTools, c.Name) {
		return nil, forbidden(fmt.Sprintf("library.call runs only %v, not %q", libraryTools, c.Name))
	}
	info, err := s.rn.Registry.GetInstance(s.ctx, s.instanceID)
	if err != nil {
		return nil, err
	}
	if !info.LibraryAccess {
		return nil, forbidden("Library access is off for this instance; the owner can turn it on in its settings")
	}
	return mcp.Call(s.ctx, s.rn.API, tier.Read, c.Name, c.Arguments)
}

func forbidden(msg string) *protocol.Error {
	return &protocol.Error{Code: protocol.CodeForbidden, Message: msg}
}

// orEmpty makes a nil slice encode as [] instead of null.
func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
