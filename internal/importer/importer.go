// Package importer runs import jobs: it calls an import plugin's import.run
// and stores the cards and lorebooks the plugin emits. It also runs catalog
// ingests, which send the same progress and checkpoint notifications, and the
// job that detects imported items' sources.
package importer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/internal/plugins"
	"github.com/davidgibbons/innkeeper/protocol"
)

// Kind is the job kind for imports. The job's instance is the import plugin
// instance, and its payload is a Payload.
const Kind = "import"

// IngestKind is the job kind for catalog ingests. The job's instance is the
// catalog plugin instance, and its payload is empty.
const IngestKind = "catalog.ingest"

// maxErrors is how many item errors a job's progress keeps.
const maxErrors = 20

// Payload is an import job's payload. Params go to the plugin unchanged.
type Payload struct {
	Params json.RawMessage `json:"params"`
}

// Importer runs import and catalog ingest jobs.
type Importer struct {
	Lib      *library.Library
	Registry *plugins.Registry
	// Queue gets a DetectKind job after each import.
	Queue *jobs.Queue
	// Plugins runs the import and catalog plugins. Its core handler must be Handle.
	Plugins *pluginhost.Manager
	Log     *slog.Logger

	mu   sync.Mutex
	runs map[string]*session
}

func New(lib *library.Library, reg *plugins.Registry, q *jobs.Queue, log *slog.Logger) *Importer {
	return &Importer{Lib: lib, Registry: reg, Queue: q, Log: log, runs: map[string]*session{}}
}

// progress is an import job's progress. Done, Total, and Message come from
// the plugin; the counts are the core's.
type progress struct {
	Done     int64    `json:"done,omitempty"`
	Total    int64    `json:"total,omitempty"`
	Message  string   `json:"message,omitempty"`
	Imported int64    `json:"imported"`
	Skipped  int64    `json:"skipped"`
	Failed   int64    `json:"failed"`
	Errors   []string `json:"errors,omitempty"`
}

// ingestProgress is a catalog ingest's progress: Done, Total, and Message
// from the plugin, and once it finishes, the counts catalog.ingest returns.
type ingestProgress struct {
	Done    int64  `json:"done,omitempty"`
	Total   int64  `json:"total,omitempty"`
	Message string `json:"message,omitempty"`
	*protocol.CatalogIngestResult
}

// session is one running import.run or catalog.ingest call.
type session struct {
	im     *Importer
	run    *jobs.Run
	plugin string
	stores bool // the job's method.stores
	ctx    context.Context
	cancel context.CancelCauseFunc

	mu sync.Mutex
	p  progress
	// result is what catalog.ingest returned.
	result *protocol.CatalogIngestResult
	err    error
}

// method is the plugin method a job kind calls.
type method struct {
	name   string
	params func(run string, p Payload, checkpoint json.RawMessage) any
	// stores is whether the core stores the items the plugin emits. A
	// catalog emits none; adopt adds its items one at a time.
	stores bool
}

var methods = map[string]method{
	Kind: {protocol.MethodImportRun, func(run string, p Payload, checkpoint json.RawMessage) any {
		return protocol.ImportRunParams{Run: run, Params: p.Params, Checkpoint: checkpoint}
	}, true},
	IngestKind: {protocol.MethodCatalogIngest, func(run string, _ Payload, checkpoint json.RawMessage) any {
		return protocol.CatalogIngestParams{Run: run, Checkpoint: checkpoint}
	}, false},
}

// Run is the jobs.Handler for Kind and IngestKind. It calls the kind's
// method on the job's instance.
func (im *Importer) Run(ctx context.Context, r *jobs.Run) error {
	m, ok := methods[r.Kind]
	if !ok {
		return fmt.Errorf("importer can't run %s jobs", r.Kind)
	}
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
	s := &session{im: im, run: r, plugin: inst.Manifest.Name, stores: m.stores, ctx: ctx, cancel: cancel}
	// ponytail: items re-sent after a resume count as skipped a second time.
	_ = json.Unmarshal(r.Progress, &s.p)

	// The token includes the attempt, so notifications from an abandoned
	// earlier attempt can't reach this one.
	token := fmt.Sprintf("%d.%d", r.ID, r.Attempts)
	im.mu.Lock()
	im.runs[token] = s
	im.mu.Unlock()
	defer func() {
		im.mu.Lock()
		delete(im.runs, token)
		im.mu.Unlock()
	}()

	// Only catalog.ingest has a result worth keeping: its counts.
	var res protocol.CatalogIngestResult
	var result any
	if !m.stores {
		result = &res
	}
	// ponytail: a run abandoned on shutdown or a lost lease keeps going in
	// the plugin until it finishes or the plugin is stopped.
	err = im.Registry.Call(ctx, im.Plugins, inst, m.name, m.params(token, payload, r.Checkpoint), result)
	if m.stores {
		EnqueueDetect(ctx, im.Queue, im.Log)
	}
	if err == nil && result != nil {
		s.mu.Lock()
		s.result = &res
		s.mu.Unlock()
		s.publish()
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

// Handle is the core handler for the plugin manager that runs imports. It
// sends each notification to the run its run token names.
func (im *Importer) Handle(_ context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case protocol.NotifyEmitCard, protocol.NotifyEmitLorebook, protocol.NotifyProgress, protocol.NotifyCheckpoint:
	default:
		return nil, protocol.MethodNotFound(method)
	}
	var head struct {
		Run string `json:"run"`
	}
	_ = json.Unmarshal(params, &head)
	im.mu.Lock()
	s := im.runs[head.Run]
	im.mu.Unlock()
	if s == nil {
		im.Log.Warn("notification for a run that is not active", "method", method, "run", head.Run)
		return nil, nil
	}
	s.handle(method, params)
	return nil, nil
}

// handle runs on the plugin connection's reading goroutine, so notifications
// are handled one at a time in the order sent. A checkpoint is therefore
// saved only after every item sent before it is stored.
func (s *session) handle(method string, params json.RawMessage) {
	if s.ctx.Err() != nil {
		return
	}
	if !s.stores && (method == protocol.NotifyEmitCard || method == protocol.NotifyEmitLorebook) {
		s.im.Log.Warn("catalog sent an item during ingest; only adopt adds catalog items", "plugin", s.plugin, "method", method)
		return
	}
	switch method {
	case protocol.NotifyEmitCard:
		var e protocol.EmitCard
		if err := json.Unmarshal(params, &e); err != nil {
			s.count(e.Item, false, &badItem{err})
			break
		}
		created, err := s.im.ingest(s.ctx, s.plugin, e.Item, library.PluginItem{Kind: library.Card, Data: e.Card, Avatar: e.Avatar})
		s.count(e.Item, created, err)
	case protocol.NotifyEmitLorebook:
		var e protocol.EmitLorebook
		if err := json.Unmarshal(params, &e); err != nil {
			s.count(e.Item, false, &badItem{err})
			break
		}
		created, err := s.im.ingest(s.ctx, s.plugin, e.Item, library.PluginItem{Kind: library.Lorebook, Data: e.Lorebook, Name: "lorebook"})
		s.count(e.Item, created, err)
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
		s.publish()
		if err := s.run.SaveCheckpoint(s.ctx, c.Checkpoint); err != nil {
			s.fail(retryable(err))
		}
		return
	}
	s.publish()
}

func (s *session) count(item string, created bool, err error) {
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
	case created:
		s.p.Imported++
	default:
		s.p.Skipped++
	}
}

func (s *session) publish() {
	s.mu.Lock()
	var raw []byte
	if !s.stores {
		raw, _ = json.Marshal(ingestProgress{Done: s.p.Done, Total: s.p.Total, Message: s.p.Message, CatalogIngestResult: s.result})
	} else {
		raw, _ = json.Marshal(s.p)
	}
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
