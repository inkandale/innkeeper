package api

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/inkandale/innkeeper/internal/actions"
	"github.com/inkandale/innkeeper/internal/backup"
	"github.com/inkandale/innkeeper/internal/bulk"
	"github.com/inkandale/innkeeper/internal/enrich"
	"github.com/inkandale/innkeeper/internal/importer"
	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/maintenance"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/internal/targets"
	"github.com/inkandale/innkeeper/internal/tier"
	"github.com/inkandale/innkeeper/protocol"
)

// watchInterval is how often a job stream checks the job. Tests shorten it.
var watchInterval = time.Second

// pingInterval is how long a quiet job stream waits before writing a comment,
// so proxies don't drop it as idle.
var pingInterval = 15 * time.Second

type jobOut struct {
	ID           int64           `json:"id"`
	Kind         string          `json:"kind"`
	Label        string          `json:"label" doc:"The kind in words, such as Push to app; a bulk job's names its action. An unknown kind's label is the kind."`
	InstanceID   *int64          `json:"instance_id,omitempty"`
	Payload      json.RawMessage `json:"payload"`
	Status       string          `json:"status" enum:"queued,running,waiting,succeeded,failed"`
	Attempts     int             `json:"attempts"`
	MaxAttempts  int             `json:"max_attempts"`
	Progress     json.RawMessage `json:"progress" doc:"An import's has done, total, message, imported, skipped, failed, and errors. A running reconcile's has done and total; a finished one's is the snapshot, cut to totals in job lists and streams. An action run's has message, done, and total, and once finished, result, which job lists and streams leave out. An enrich run's has message, done, failed, total, and warnings."`
	ParentID     *int64          `json:"parent_id,omitempty"`
	Error        *string         `json:"error,omitempty"`
	RunAfter     time.Time       `json:"run_after"`
	CreatedAt    time.Time       `json:"created_at"`
	FinishedAt   *time.Time      `json:"finished_at,omitempty"`
	StartedAt    *time.Time      `json:"started_at,omitempty" doc:"When the first attempt started; a bulk job's is its earliest child's. A manual retry clears it."`
	DurationMS   *int64          `json:"duration_ms,omitempty" doc:"Milliseconds from started_at to finished_at, once the job finishes."`
	CardsChanged int             `json:"cards_changed" doc:"Cards the job changed in the library or an app, across every attempt; a bulk job's sums its children's."`
	Trigger      string          `json:"trigger" enum:"manual,scheduled,automatic" doc:"What started the job: a person, the worker's schedule, or another job, such as a sync's pushes."`
	Children     map[string]int  `json:"children,omitempty" doc:"A bulk job's children, counted by status. Get-job, cancel-job, watch-job, and list-jobs fill it."`
	Outcomes     map[string]int  `json:"outcomes,omitempty" doc:"A bulk job's finished children, counted by outcome, such as a resolve's action, stale, conflict, or failed. Only get-job, cancel-job, and watch-job fill it."`
}

// kindLabels name each job kind for people.
var kindLabels = map[string]string{
	importer.Kind:              "Import",
	importer.DetectKind:        "Find sources",
	actions.Kind:               "Run action",
	enrich.Kind:                "Score cards",
	enrich.SweepKind:           "Catch up changed cards",
	bulk.ArchiveKind:           "Archive card",
	bulk.LorebookArchiveKind:   "Archive lorebook",
	bulk.TagKind:               "Tag card",
	bulk.CollectKind:           "Change collections",
	bulk.ExportKind:            "Export cards",
	bulk.DeleteLibraryKind:     "Delete library",
	bulk.PurgeKind:             "Purge cards",
	bulk.HideKind:              "Hide duplicates",
	targets.PushKind:           "Push to app",
	targets.ScanKind:           "Check apps for changes",
	targets.ReconcileKind:      "Sync app",
	targets.ResolveKind:        "Apply review choice",
	targets.SyncKind:           "Sync every app",
	targets.SourceCheckKind:    "Check sources for updates",
	backup.Kind(backup.Assets): "Back up library",
	backup.Kind(backup.Config): "Back up settings",
	backup.RestoreKind:         "Restore backup",
	library.GarbageKind:        "Clean up unused files",
	jobs.PruneKind:             "Clean up old jobs",
	maintenance.Kind:           "Maintenance",
}

// bulkLabels name a bulk job by its action, the kind of its children.
var bulkLabels = map[string]string{
	bulk.ArchiveKind:         "Archive cards",
	bulk.LorebookArchiveKind: "Archive lorebooks",
	bulk.TagKind:             "Tag cards",
	bulk.CollectKind:         "Change collections",
	bulk.ExportKind:          "Export cards",
	targets.ResolveKind:      "Apply review choices",
	targets.PushKind:         "Push to app",
}

func kindLabel(kind string, payload json.RawMessage) string {
	if kind != bulk.Kind {
		return cmp.Or(kindLabels[kind], kind)
	}
	var p struct {
		Action   string
		Archived *bool
	}
	if json.Unmarshal(payload, &p) != nil || bulkLabels[p.Action] == "" {
		return "Bulk change"
	}
	if p.Archived != nil && !*p.Archived {
		return "Un" + strings.ToLower(bulkLabels[p.Action][:1]) + bulkLabels[p.Action][1:]
	}
	return bulkLabels[p.Action]
}

func toJob(j jobs.Job) jobOut {
	return jobOut{ID: j.ID, Kind: j.Kind, Label: kindLabel(j.Kind, j.Payload), InstanceID: j.InstanceID, Payload: j.Payload, Status: j.Status,
		Attempts: j.Attempts, MaxAttempts: j.MaxAttempts, Progress: j.Progress, ParentID: j.ParentID, Error: j.Error,
		RunAfter: j.RunAfter, CreatedAt: j.CreatedAt, FinishedAt: j.FinishedAt, StartedAt: j.StartedAt,
		DurationMS: durationMS(j.StartedAt, j.FinishedAt), CardsChanged: j.CardsChanged, Trigger: j.Trigger}
}

func durationMS(start, end *time.Time) *int64 {
	if start == nil || end == nil {
		return nil
	}
	return new(end.Sub(*start).Milliseconds())
}

// withChildren fills a bulk job's counts, start, and cards from its children.
func (o *jobOut) withChildren(c jobs.Children) {
	o.Children, o.StartedAt, o.CardsChanged = c.Counts, c.StartedAt, c.CardsChanged
	o.DurationMS = durationMS(o.StartedAt, o.FinishedAt)
}

// summary is a job as lists and streams show it: a finished reconcile's
// snapshot is cut to its totals, an export's card IDs to a count, and an
// action run's result is left out. Get the job for the whole of each.
func summary(j jobOut) jobOut {
	switch j.Kind {
	case targets.ReconcileKind:
		var snap struct {
			Totals map[string]int `json:"totals"`
		}
		if json.Unmarshal(j.Progress, &snap) == nil && snap.Totals != nil {
			j.Progress, _ = json.Marshal(map[string]any{"totals": snap.Totals})
		}
	case actions.Kind:
		j.Progress = actions.ListProgress(j.Progress)
	case bulk.ExportKind:
		var p struct {
			CardIDs []int64 `json:"card_ids"`
			Format  string  `json:"format"`
		}
		if json.Unmarshal(j.Payload, &p) == nil {
			j.Payload, _ = json.Marshal(map[string]any{"format": p.Format, "cards": len(p.CardIDs)})
		}
	}
	return j
}

type jobResp struct{ Body jobOut }

type jobsOut struct{ Body []jobOut }

type listJobsIn struct {
	Status     string   `query:"status" enum:"queued,running,waiting,succeeded,failed"`
	Kind       string   `query:"kind"`
	Plugin     string   `query:"plugin" doc:"Only jobs run by an instance of this plugin"`
	Trigger    []string `query:"trigger,explode" enum:"manual,scheduled,automatic" doc:"Only jobs started by one of these triggers; repeat it to list several"`
	InstanceID int64    `query:"instance_id" doc:"Only jobs run by this instance"`
	Parent     int64    `query:"parent" doc:"List this bulk job's children instead of top-level jobs"`
	CardID     int64    `query:"card_id" doc:"Only top-level jobs whose payload names this card, such as its pushes and action runs"`
	LorebookID int64    `query:"lorebook_id" doc:"Only top-level jobs whose payload names this lorebook, such as its action runs. Not with card_id"`
	Before     int64    `query:"before" doc:"Only jobs with smaller IDs, for the next page"`
	Limit      int      `query:"limit" default:"50" minimum:"1" maximum:"200"`
}

type jobLogsIn struct {
	ID    int64 `path:"id"`
	After int64 `query:"after" doc:"Only lines after this line ID, to fetch what's new"`
	Limit int   `query:"limit" default:"500" minimum:"1" maximum:"1000"`
}

type jobLogsOut struct{ Body []jobs.LogLine }

type importIn struct {
	Body struct {
		InstanceID int64           `json:"instance_id"`
		Params     json.RawMessage `json:"params,omitempty" doc:"The import plugin's settings, such as a folder path."`
		Library    int64           `json:"library,omitempty" doc:"The library to import into; default 1"`
	}
}

type bulkIn struct {
	Body struct {
		Query      library.Query `json:"query" doc:"Selects the cards, as in POST /cards/search. Paging is ignored. To pick cards by ID, filter on ids."`
		Kind       string        `json:"kind,omitempty" enum:"card,lorebook" doc:"What the query selects; default card. Lorebooks take archive and unarchive only."`
		Action     string        `json:"action" enum:"archive,unarchive,tag,untag,collect,uncollect,export"`
		Tags       []string      `json:"tags,omitempty" maxItems:"100" maxLength:"100" doc:"For tag and untag: the tags to add or remove."`
		Collection int64         `json:"collection,omitempty" doc:"For collect and uncollect: the collection."`
		Format     string        `json:"format,omitempty" doc:"For export: json (the default), or a format a codec plugin instance writes, such as png."`
		Unpublish  bool          `json:"unpublish,omitempty" doc:"For archive: also delete each one's copies from the apps it was pushed to. An app that can't delete keeps its copy."`
	}
}

// usable checks that an instance exists, isn't broken or disabled, and its
// plugin has capability, and returns it.
func (s *Server) usable(ctx context.Context, id int64, capability string) (pluginhost.Instance, error) {
	inst, err := s.Registry.InstanceWith(ctx, id, capability)
	return inst, s.fail(err)
}

// enqueue queues a job and answers with it.
func (s *Server) enqueue(ctx context.Context, spec jobs.Spec) (*jobResp, error) {
	j, err := s.Queue.Enqueue(ctx, spec)
	if err != nil {
		return nil, s.fail(err)
	}
	return &jobResp{Body: toJob(j)}, nil
}

// job returns a job, with child counts and outcomes for a bulk job.
func (s *Server) job(ctx context.Context, id int64) (jobOut, error) {
	j, err := s.Queue.Get(ctx, id)
	if err != nil {
		return jobOut{}, s.fail(err)
	}
	out := toJob(j)
	if j.Kind == bulk.Kind {
		children, err := s.Queue.ChildrenFor(ctx, []int64{id})
		if err != nil {
			return jobOut{}, s.fail(err)
		}
		if c, ok := children[id]; ok {
			out.withChildren(c)
		}
		if out.Outcomes, err = s.Queue.ChildOutcomes(ctx, id); err != nil {
			return jobOut{}, s.fail(err)
		}
	}
	return out, nil
}

// watch sends the job as an SSE event each time it changes, until it
// finishes, the client goes away, or the server stops.
//
// ponytail: polls the database every watchInterval per watcher; use
// LISTEN/NOTIFY if many clients watch at once.
func (s *Server) watch(hc huma.Context, j jobOut) {
	ctx := hc.Context()
	hc.SetHeader("Content-Type", "text/event-stream")
	hc.SetHeader("Cache-Control", "no-cache")
	hc.SetHeader("X-Accel-Buffering", "no")
	w := hc.BodyWriter()
	flusher, _ := w.(http.Flusher)
	t := time.NewTicker(watchInterval)
	defer t.Stop()
	var last []byte
	var wrote time.Time
	send := func(format string, args ...any) bool {
		if _, err := fmt.Fprintf(w, format, args...); err != nil {
			return false
		}
		if flusher != nil {
			flusher.Flush()
		}
		wrote = time.Now()
		return true
	}
	for {
		data, err := json.Marshal(summary(j))
		if err != nil {
			return
		}
		if !bytes.Equal(data, last) {
			if !send("event: job\ndata: %s\n\n", data) {
				return
			}
			last = data
		} else if time.Since(wrote) >= pingInterval && !send(": ping\n\n") {
			return
		}
		if j.Status == jobs.StatusSucceeded || j.Status == jobs.StatusFailed {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-s.Stopping:
			return
		case <-t.C:
		}
		if j, err = s.job(ctx, j.ID); err != nil {
			return
		}
	}
}

func (s *Server) jobRoutes(api huma.API) {
	tags := []string{"Jobs"}
	huma.Register(api, huma.Operation{OperationID: "list-jobs", Method: http.MethodGet, Path: "/jobs",
		Summary: "List jobs", Description: "Newest first. A finished reconcile's progress is cut to its totals, an export's card_ids to a count of cards, " +
			"and an action run's result is left out; get the job for the whole of each.", Tags: tags},
		func(ctx context.Context, in *listJobsIn) (*jobsOut, error) {
			o := jobs.ListOptions{Status: in.Status, Kind: in.Kind, Plugin: in.Plugin, Triggers: in.Trigger, Instance: in.InstanceID, Before: in.Before, Limit: in.Limit}
			if in.Parent != 0 {
				o.Parent = &in.Parent
			}
			switch {
			case in.CardID != 0 && in.LorebookID != 0:
				return nil, huma.Error400BadRequest("filter by card_id or lorebook_id, not both")
			case in.CardID != 0:
				o.Payload = fmt.Appendf(nil, `{"card_id": %d}`, in.CardID)
			case in.LorebookID != 0:
				o.Payload = fmt.Appendf(nil, `{"lorebook_id": %d}`, in.LorebookID)
			}
			list, err := s.Queue.List(ctx, o)
			if err != nil {
				return nil, s.fail(err)
			}
			var parents []int64
			for _, j := range list {
				if j.Kind == bulk.Kind {
					parents = append(parents, j.ID)
				}
			}
			children, err := s.Queue.ChildrenFor(ctx, parents)
			if err != nil {
				return nil, s.fail(err)
			}
			out := &jobsOut{Body: make([]jobOut, len(list))}
			for i, j := range list {
				out.Body[i] = summary(toJob(j))
				if c, ok := children[j.ID]; ok {
					out.Body[i].withChildren(c)
				}
			}
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "get-job", Method: http.MethodGet, Path: "/jobs/{id}",
		Summary: "Get a job", Tags: tags},
		func(ctx context.Context, in *idIn) (*jobResp, error) {
			j, err := s.job(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			return &jobResp{Body: j}, nil
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Write), OperationID: "cancel-job", Method: http.MethodPost, Path: "/jobs/{id}/cancel",
		Summary: "Cancel a job", Description: "Fails the job and its unfinished children. A running job stops within seconds.", Tags: tags},
		func(ctx context.Context, in *idIn) (*jobResp, error) {
			if err := s.Queue.Cancel(ctx, in.ID); err != nil {
				return nil, s.fail(err)
			}
			j, err := s.job(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			return &jobResp{Body: j}, nil
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Write), OperationID: "retry-job", Method: http.MethodPost, Path: "/jobs/{id}/retry",
		Summary: "Retry a failed job", Description: "Queues a failed job again with all its attempts. An import picks up from its last checkpoint. " +
			"A bulk job and its children can't be retried this way.", Tags: tags},
		func(ctx context.Context, in *idIn) (*jobResp, error) {
			if err := s.Queue.Retry(ctx, in.ID); err != nil {
				return nil, s.fail(err)
			}
			j, err := s.job(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			return &jobResp{Body: j}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "list-job-logs", Method: http.MethodGet, Path: "/jobs/{id}/logs",
		Summary: "List a job's log", Description: "Oldest first: what the worker and the job's plugin logged while it ran. " +
			"A job keeps its last 1000 lines.", Tags: tags},
		func(ctx context.Context, in *jobLogsIn) (*jobLogsOut, error) {
			if _, err := s.Queue.Get(ctx, in.ID); err != nil {
				return nil, s.fail(err)
			}
			lines, err := s.Queue.Logs(ctx, in.ID, in.After, in.Limit)
			if err != nil {
				return nil, s.fail(err)
			}
			return &jobLogsOut{Body: lines}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "watch-job", Method: http.MethodGet, Path: "/jobs/{id}/events",
		Summary: "Stream a job's progress",
		Description: "Server-sent events: a job event with the whole job each time it changes. The stream ends when the job " +
			"finishes. A reconcile's progress, an export's card IDs, and an action run's result are cut as in the job list; get the job for the whole of each.",
		Tags: tags,
		Responses: map[string]*huma.Response{"200": {Description: "A stream of job events",
			Content: map[string]*huma.MediaType{"text/event-stream": {}}}}},
		func(ctx context.Context, in *idIn) (*huma.StreamResponse, error) {
			first, err := s.job(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			return &huma.StreamResponse{Body: func(hc huma.Context) { s.watch(hc, first) }}, nil
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Write), OperationID: "start-import", Method: http.MethodPost, Path: "/imports",
		Summary:     "Start an import",
		Description: "Queues an import job for an instance of an import plugin.",
		Tags:        tags, DefaultStatus: http.StatusAccepted},
		func(ctx context.Context, in *importIn) (*jobResp, error) {
			inst, err := s.Registry.GetInstance(ctx, in.Body.InstanceID)
			if err != nil {
				return nil, s.fail(err)
			}
			if !s.Registry.HasCapability(inst.Plugin, protocol.CapabilityImport) {
				if s.Registry.HasCapability(inst.Plugin, protocol.CapabilityPush) {
					return nil, huma.Error400BadRequest(fmt.Sprintf("plugin %s has no importer; sync the app with POST /reconciles", inst.Plugin))
				}
				return nil, huma.Error400BadRequest(fmt.Sprintf("plugin %s can't import", inst.Plugin))
			}
			if _, err := s.usable(ctx, in.Body.InstanceID, protocol.CapabilityImport); err != nil {
				return nil, err
			}
			lib, err := s.checkLibrary(ctx, in.Body.Library)
			if err != nil {
				return nil, err
			}
			params := in.Body.Params
			if params == nil {
				params = json.RawMessage(`{}`)
			}
			return s.enqueue(ctx, jobs.Spec{Kind: importer.Kind, InstanceID: &in.Body.InstanceID,
				Payload: importer.Payload{Params: params, Library: lib}})
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Write), OperationID: "start-bulk", Method: http.MethodPost, Path: "/bulk",
		Summary: "Archive, tag, collect, or export many cards, or archive many lorebooks",
		Description: "Queues one child job per card the query matches. Export queues one job that writes a zip; " +
			"its progress names the zip's blob when it's done, and the blob is deleted after a day.",
		Tags: tags, DefaultStatus: http.StatusAccepted},
		func(ctx context.Context, in *bulkIn) (*jobResp, error) {
			b := in.Body
			q := b.Query
			if q.Filter == nil && q.Text == "" {
				return nil, huma.Error400BadRequest("bulk actions need a filter or text; to select every card, filter on archived")
			}
			lorebooks := b.Kind == "lorebook"
			if lorebooks && b.Action != "archive" && b.Action != "unarchive" {
				return nil, huma.Error400BadRequest("lorebooks can only be archived or unarchived in bulk; push them with POST /pushes")
			}
			if b.Unpublish && b.Action != "archive" {
				return nil, huma.Error400BadRequest("unpublish only goes with archive")
			}
			switch b.Action {
			case "archive", "unarchive":
				// Select only cards the action changes; search would otherwise hide archived cards from unarchive.
				wasArchived := b.Action == "unarchive"
				state := library.Filter{Archived: &wasArchived}
				if q.Filter != nil {
					state = library.Filter{And: []library.Filter{*q.Filter, state}}
				}
				q.Filter = &state
			case "tag", "untag":
				if len(b.Tags) == 0 {
					return nil, huma.Error400BadRequest(b.Action + " needs tags")
				}
				if slices.ContainsFunc(b.Tags, func(t string) bool { return strings.TrimSpace(t) == "" }) {
					return nil, huma.Error400BadRequest("tags can't be blank")
				}
			case "collect", "uncollect":
				lib, err := s.checkLibrary(ctx, q.Library)
				if err != nil {
					return nil, err
				}
				cols, err := s.Lib.Collections(ctx, lib)
				if err != nil {
					return nil, s.fail(err)
				}
				if !slices.ContainsFunc(cols, func(c library.Collection) bool { return c.ID == b.Collection }) {
					return nil, huma.Error404NotFound("collection not found")
				}
			case "export":
				if b.Format == "" {
					b.Format = "json"
				}
				if b.Format != "json" {
					if _, err := s.Registry.CodecInstance(ctx, b.Format); err != nil {
						return nil, s.fail(err)
					}
				}
			}
			ids, err := s.searchIDs(ctx, kindNamed(b.Kind), q)
			if err != nil {
				return nil, s.fail(err)
			}
			var p jobs.Job
			switch b.Action {
			case "archive", "unarchive":
				if lorebooks {
					p, err = bulk.EnqueueLorebookArchive(ctx, s.Queue, ids, b.Action == "archive", b.Unpublish)
				} else {
					p, err = bulk.EnqueueArchive(ctx, s.Queue, ids, b.Action == "archive", b.Unpublish)
				}
			case "tag":
				p, err = bulk.EnqueueTag(ctx, s.Queue, ids, b.Tags, nil)
			case "untag":
				p, err = bulk.EnqueueTag(ctx, s.Queue, ids, nil, b.Tags)
			case "collect", "uncollect":
				p, err = bulk.EnqueueCollect(ctx, s.Queue, ids, b.Collection, b.Action == "uncollect")
			case "export":
				p, err = bulk.EnqueueExport(ctx, s.Queue, ids, b.Format)
			}
			if err != nil {
				return nil, s.fail(err)
			}
			j, err := s.job(ctx, p.ID)
			if err != nil {
				return nil, err
			}
			return &jobResp{Body: j}, nil
		})
}
