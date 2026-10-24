package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sync"

	"github.com/inkandale/innkeeper/internal/enrich"
	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/mcp"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/internal/tier"
	"github.com/inkandale/innkeeper/protocol"
)

// libraryTools are the MCP tools library.call serves: the read tools that
// read the library, not apps or settings.
var libraryTools = []string{"search_cards", "get_card", "get_lorebook", "get_history"}

// Runner runs action jobs.
type Runner struct {
	plugins.JobRunner
	Lib *library.Library
	// API serves library.call's tools in process; see mcp.Call.
	API http.Handler
}

// Run is the jobs.Handler for Kind. It runs once: a run costs the owner
// money, so running it again is the owner's choice.
func (rn *Runner) Run(ctx context.Context, r *jobs.Run) error { return jobs.Once(rn.run)(ctx, r) }

func (rn *Runner) run(ctx context.Context, r *jobs.Run) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var p Payload
	pr, err := rn.OpenRun(ctx, r, protocol.CapabilityAction, &p)
	if err != nil {
		return err
	}
	defer pr.Close()
	inst := pr.Instance
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
	s := &session{rn: rn, ctx: ctx, run: pr, action: a, instanceID: *r.InstanceID}
	params.Run = pr.Serve(s.progress, s.serve)

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
	warnings = append(warnings, rn.storeValues(ctx, inst, *r.InstanceID, p, res)...)
	s.mu.Lock()
	s.p.Result = &Result{Proposal: proposal, Report: res.Report, Raw: res.Raw, Warnings: orEmpty(warnings)}
	s.mu.Unlock()
	s.publish()
	return nil
}

// storeValues stores res's enrichment values on the run's card when its head
// revision holds the data the run had, and returns why it stored fewer.
// Failures are warnings, so the owner still gets the report they paid for.
func (rn *Runner) storeValues(ctx context.Context, inst pluginhost.Instance, instanceID int64, p Payload, res protocol.ActionRunResult) []string {
	if len(res.Values) == 0 || p.CardID == 0 {
		return nil
	}
	c, err := rn.Lib.Cards.Get(ctx, p.CardID)
	var head library.Revision
	if err == nil {
		head, err = rn.Lib.Cards.Revision(ctx, c.HeadRevisionID)
	}
	if err != nil {
		return []string{fmt.Sprintf("kept no scores: %v", err)}
	}
	// A draft with unsaved edits matches no revision.
	if !sameValue(head.Data, p.Entity.Data) {
		return nil
	}
	sent := protocol.EnrichRevision{CardID: p.CardID, RevisionID: head.ID, Data: head.Data}
	checked := enrich.Check(inst.Manifest.Enrichments, []protocol.EnrichRevision{sent},
		protocol.EnrichRunResult{Results: []protocol.EnrichResult{{RevisionID: head.ID, Values: res.Values, Meta: res.Meta}}})
	if len(checked.Values) == 0 {
		return checked.Warnings
	}
	if err := rn.Lib.AddEnrichments(ctx, instanceID, checked.Values); err != nil {
		return append(checked.Warnings, fmt.Sprintf("storing the scores failed: %v", err))
	}
	return checked.Warnings
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
	run        *plugins.PluginRun
	action     protocol.Action
	instanceID int64

	mu sync.Mutex
	p  Progress
}

func (s *session) progress(p protocol.Progress) {
	s.mu.Lock()
	s.p.Message, s.p.Done, s.p.Total = p.Message, p.Done, p.Total
	s.mu.Unlock()
	s.publish()
}

// serve answers the plugin's calls for this run other than progress.
func (s *session) serve(_ context.Context, method string, params json.RawMessage) (any, error) {
	if method == protocol.MethodLibraryCall {
		return s.libraryCall(params)
	}
	return nil, protocol.MethodNotFound(method)
}

func (s *session) publish() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.run.Publish(s.p)
}

// libraryCall runs a library tool for the plugin, if its action declared
// library and the owner turned on Library access for the instance.
func (s *session) libraryCall(params json.RawMessage) (any, error) {
	var c protocol.LibraryCallParams
	if err := json.Unmarshal(params, &c); err != nil {
		return nil, protocol.InvalidParams(err)
	}
	if !s.action.Library {
		return nil, forbidden(fmt.Sprintf("action %s doesn't declare library", s.action.ID))
	}
	// Stopping a run cancels its context before the token is removed.
	if s.ctx.Err() != nil {
		return nil, forbidden("the run has ended")
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
