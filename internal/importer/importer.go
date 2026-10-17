// Package importer runs import jobs: it calls an import plugin's import.run
// and stores the cards and lorebooks the plugin emits. It also runs the job
// that detects imported items' sources.
package importer

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"

	"github.com/jackc/pgx/v5"

	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/internal/targets"
	"github.com/inkandale/innkeeper/protocol"
)

// Kind is the job kind for imports. The job's instance is the import plugin
// instance, and its payload is a Payload.
const Kind = "import"

// maxErrors is how many item errors a job's progress keeps.
const maxErrors = 20

// Payload is an import job's payload. Params go to the plugin unchanged.
type Payload struct {
	Params json.RawMessage `json:"params"`
	// Library is where the items go; 0 means library.DefaultLibrary.
	Library int64 `json:"library,omitempty"`
}

// Importer runs import jobs.
type Importer struct {
	Lib      *library.Library
	Registry *plugins.Registry
	// Queue gets a DetectKind job after each import.
	Queue *jobs.Queue
	// Plugins runs the import plugins. Its core handler must be
	// Runs.Handle.
	Plugins *pluginhost.Manager
	Runs    *pluginhost.Runs
	// Targets applies an import's waiting updates whose binding has autosync on.
	Targets *targets.Service
	Log     *slog.Logger
}

func New(lib *library.Library, reg *plugins.Registry, q *jobs.Queue, mgr *pluginhost.Manager, runs *pluginhost.Runs,
	tg *targets.Service, log *slog.Logger) *Importer {
	return &Importer{Lib: lib, Registry: reg, Queue: q, Plugins: mgr, Runs: runs, Targets: tg, Log: log}
}

// progress is an import job's progress. Done, Total, and Message come from
// the plugin; the counts are the core's.
type progress struct {
	Done     int64  `json:"done,omitempty"`
	Total    int64  `json:"total,omitempty"`
	Message  string `json:"message,omitempty"`
	Imported int64  `json:"imported"`
	// Revised counts items that became a new revision of their card.
	Revised int64 `json:"revised"`
	// Pending counts items left as a source update on a card the owner edited.
	Pending int64    `json:"pending"`
	Skipped int64    `json:"skipped"`
	Failed  int64    `json:"failed"`
	Errors  []string `json:"errors,omitempty"`
	// Warnings are problems that cost an item part of what it sent, such as an undeclared value.
	Warnings []string `json:"warnings,omitempty"`
}

// session is one running import.run call.
type session struct {
	im     *Importer
	run    *jobs.Run
	plugin string
	ctx    context.Context
	cancel context.CancelCauseFunc
	// library and instance are where items go and which instance sent them.
	library, instance int64
	// bulk stores items a checkpoint at a time; buf holds those since the last.
	bulk bool
	buf  []buffered
	// autosync holds stored updates for autosync to try once the plugin call
	// returns: applying one waits for a running sync, which must not stall
	// reading the plugin's notifications.
	autosync []int64
	// decl is the plugin's [enrichments], which values must match.
	decl map[string]protocol.Enrichment

	mu  sync.Mutex
	p   progress
	err error
}

// Run is the jobs.Handler for Kind. It calls import.run on the job's instance.
func (im *Importer) Run(ctx context.Context, r *jobs.Run) error {
	if r.InstanceID == nil {
		return fmt.Errorf("%s job has no plugin instance", r.Kind)
	}
	inst, err := im.Registry.Instance(ctx, *r.InstanceID)
	if err != nil {
		return err
	}
	var payload Payload
	if err := json.Unmarshal(r.Payload, &payload); err != nil {
		return fmt.Errorf("%s payload: %w", r.Kind, err)
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	s := &session{im: im, run: r, plugin: inst.Manifest.Name, ctx: ctx, cancel: cancel,
		library: cmp.Or(payload.Library, library.DefaultLibrary), instance: *r.InstanceID, bulk: inst.Manifest.Bulk,
		decl: inst.Manifest.Enrichments}
	// ponytail: items re-sent after a resume count as skipped a second time.
	_ = json.Unmarshal(r.Progress, &s.p)

	token, remove := im.Runs.Add(inst.ID, s.serve)
	defer remove()

	// ponytail: a run abandoned on shutdown or a lost lease keeps going in
	// the plugin until it finishes or the plugin is stopped.
	params := protocol.ImportRunParams{Run: token, Params: payload.Params, Checkpoint: r.Checkpoint}
	err = im.Registry.Call(ctx, im.Plugins, inst, protocol.MethodImportRun, params, nil)
	if err == nil && len(s.buf) > 0 {
		// Items sent after the last checkpoint.
		s.flush(nil)
	}
	s.applyAutosync()
	if !s.bulk {
		EnqueueDetect(ctx, im.Queue, im.Log)
	}
	if serr := s.failure(); serr != nil {
		return serr
	}
	return plugins.JobErr(err)
}

// EnqueueDetect makes sure a DetectKind job will run. A failure is only
// logged, since detection never fails an import; the hourly run catches up.
func EnqueueDetect(ctx context.Context, q *jobs.Queue, log *slog.Logger) {
	if _, err := q.EnqueueOnce(context.WithoutCancel(ctx), jobs.Spec{Kind: DetectKind}); err != nil {
		log.Error("enqueue source detection", "err", err)
	}
}

// serve answers the plugin's calls for this run: the notifications an
// import sends.
func (s *session) serve(_ context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case protocol.NotifyEmitCard, protocol.NotifyEmitLorebook, protocol.NotifyProgress, protocol.NotifyCheckpoint:
		s.handle(method, params)
		return nil, nil
	}
	return nil, protocol.MethodNotFound(method)
}

// handle runs on the plugin connection's reading goroutine, so notifications
// are handled one at a time in the order sent. A checkpoint is therefore
// saved only after every item sent before it is stored.
func (s *session) handle(method string, params json.RawMessage) {
	if s.ctx.Err() != nil {
		return
	}
	switch method {
	case protocol.NotifyEmitCard:
		var e protocol.EmitCard
		if err := json.Unmarshal(params, &e); err != nil {
			s.count(e.Item, library.Imported{}, &badItem{err})
			break
		}
		values, problems := protocol.CheckValues(s.decl, e.Values)
		s.store(e.Item, library.PluginItem{Kind: library.Card, Data: e.Card, Avatar: e.Avatar, Version: e.Version,
			Created: e.Created, Values: values}, problems...)
	case protocol.NotifyEmitLorebook:
		var e protocol.EmitLorebook
		if err := json.Unmarshal(params, &e); err != nil {
			s.count(e.Item, library.Imported{}, &badItem{err})
			break
		}
		s.store(e.Item, library.PluginItem{Kind: library.Lorebook, Data: e.Lorebook, Name: "lorebook"})
	case protocol.NotifyProgress:
		var p protocol.Progress
		if json.Unmarshal(params, &p) == nil {
			s.mu.Lock()
			s.p.Done, s.p.Total, s.p.Message = p.Done, p.Total, p.Message
			s.mu.Unlock()
		}
	case protocol.NotifyCheckpoint:
		var c protocol.Checkpoint
		if err := json.Unmarshal(params, &c); err != nil {
			s.fail(fmt.Errorf("plugin sent an invalid checkpoint: %w", err))
			return
		}
		if s.bulk {
			s.flush(c.Checkpoint)
			return
		}
		s.publish()
		if err := s.run.SaveCheckpoint(s.ctx, c.Checkpoint); err != nil {
			s.fail(retryable(err))
		}
		return
	}
	s.publish()
}

// buffered is an item a bulk import holds until the next checkpoint, with
// the warnings it is counted with once stored.
type buffered struct {
	name     string
	it       library.PluginItem
	warnings []string
}

// store stores an item the plugin emitted under its ID for the item, or in
// bulk mode, buffers it until the next checkpoint. warnings are problems
// with what the item sent.
func (s *session) store(item string, it library.PluginItem, warnings ...string) {
	if item == "" {
		s.count(item, library.Imported{}, &badItem{errors.New("item is empty")})
		return
	}
	it.LibraryID, it.InstanceID, it.SkipDetect = s.library, s.instance, s.bulk
	it.Provenance, it.Source = s.plugin+":"+item, "plugin:"+s.plugin
	if s.bulk {
		s.buf = append(s.buf, buffered{item, it, warnings})
		return
	}
	s.warn(item, warnings)
	got, err := s.im.Lib.ImportItem(s.ctx, it)
	s.count(item, got, invalid(err))
	s.autosync = append(s.autosync, got.Autosync...)
}

// flush stores the buffered items and saves cp, which may be nil, in one
// transaction. Their counts are saved with it, and kept only if it commits.
func (s *session) flush(cp json.RawMessage) {
	buf := s.buf
	s.buf = nil
	items := make([]library.PluginItem, len(buf))
	for i, b := range buf {
		items[i] = b.it
	}
	s.mu.Lock()
	before := s.p
	before.Errors, before.Warnings = slices.Clone(s.p.Errors), slices.Clone(s.p.Warnings)
	s.mu.Unlock()
	var auto []int64
	err := s.im.Lib.ImportBatch(s.ctx, items, func(tx pgx.Tx, got []library.Imported, errs []error) error {
		for i, b := range buf {
			s.warn(b.name, b.warnings)
			s.count(b.name, got[i], invalid(errs[i]))
			auto = append(auto, got[i].Autosync...)
		}
		s.publish()
		return s.run.SaveCheckpointTx(s.ctx, tx, cp)
	})
	if err != nil {
		s.mu.Lock()
		s.p = before
		s.mu.Unlock()
		s.publish()
		s.fail(retryable(err))
		return
	}
	s.autosync = append(s.autosync, auto...)
}

// applyAutosync applies each waiting update the import left on a binding
// with autosync on. One that fails stays in the changes queue.
func (s *session) applyAutosync() {
	for _, id := range s.autosync {
		if _, err := s.im.Targets.ApplyUpdate(s.ctx, id); err != nil {
			s.warn(fmt.Sprintf("binding %d", id), []string{fmt.Sprintf("autosync: %v", err)})
		}
	}
	s.publish()
}

// warn records problems with what item sent.
func (s *session) warn(item string, problems []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range problems {
		if len(s.p.Warnings) < maxErrors {
			s.p.Warnings = append(s.p.Warnings, item+": "+p)
		}
	}
}

func (s *session) count(item string, got library.Imported, err error) {
	var bad *badItem
	if err != nil && !errors.As(err, &bad) {
		s.fail(retryable(fmt.Errorf("store %q: %w", item, err)))
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case bad != nil:
		s.p.Failed++
		if len(s.p.Errors) < maxErrors {
			s.p.Errors = append(s.p.Errors, fmt.Sprintf("%s: %v", item, bad.err))
		}
	case got.Outcome == library.Created:
		s.p.Imported++
	case got.Outcome == library.Revised:
		s.p.Revised++
	case got.Outcome == library.Pending:
		s.p.Pending++
	default:
		s.p.Skipped++
	}
}

func (s *session) publish() {
	s.mu.Lock()
	raw, _ := json.Marshal(s.p)
	s.mu.Unlock()
	s.run.SetProgress(raw)
}

// fail stops the run: the plugin call is cancelled and run returns err.
func (s *session) fail(err error) {
	s.mu.Lock()
	if s.err == nil {
		s.err = err
	}
	s.mu.Unlock()
	s.cancel(err)
}

func (s *session) failure() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// retryable marks a library or database failure as temporary. A lost lease
// is left as it is, so the worker abandons the job.
func retryable(err error) error {
	if errors.Is(err, jobs.ErrLeaseLost) {
		return err
	}
	return fmt.Errorf("%w: %w", jobs.ErrRetry, err)
}
