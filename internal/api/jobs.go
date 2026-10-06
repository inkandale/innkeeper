package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/davidgibbons/innkeeper/internal/bulk"
	"github.com/davidgibbons/innkeeper/internal/importer"
	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/plugins"
	"github.com/davidgibbons/innkeeper/internal/targets"
	"github.com/davidgibbons/innkeeper/internal/tier"
)

// watchInterval is how often a job stream checks the job. Tests shorten it.
var watchInterval = time.Second

// pingInterval is how long a quiet job stream waits before writing a comment,
// so proxies don't drop it as idle.
var pingInterval = 15 * time.Second

type jobOut struct {
	ID          int64           `json:"id"`
	Kind        string          `json:"kind"`
	InstanceID  *int64          `json:"instance_id,omitempty"`
	Payload     json.RawMessage `json:"payload"`
	Status      string          `json:"status" enum:"queued,running,waiting,succeeded,failed"`
	Attempts    int             `json:"attempts"`
	MaxAttempts int             `json:"max_attempts"`
	Progress    json.RawMessage `json:"progress" doc:"An import's has done, total, message, imported, skipped, failed, and errors. A running reconcile's has done and total; a finished one's is the snapshot, cut to totals in job lists and streams."`
	ParentID    *int64          `json:"parent_id,omitempty"`
	Error       *string         `json:"error,omitempty"`
	RunAfter    time.Time       `json:"run_after"`
	CreatedAt   time.Time       `json:"created_at"`
	FinishedAt  *time.Time      `json:"finished_at,omitempty"`
	Children    map[string]int  `json:"children,omitempty" doc:"A bulk job's children, counted by status. Get-job, cancel-job, watch-job, and list-jobs fill it."`
	Outcomes    map[string]int  `json:"outcomes,omitempty" doc:"A bulk import's finished children, counted by outcome: created, bound, already_bound, duplicate, or missing. Only get-job, cancel-job, and watch-job fill it."`
}

func toJob(j jobs.Job) jobOut {
	return jobOut{ID: j.ID, Kind: j.Kind, InstanceID: j.InstanceID, Payload: j.Payload, Status: j.Status,
		Attempts: j.Attempts, MaxAttempts: j.MaxAttempts, Progress: j.Progress, ParentID: j.ParentID, Error: j.Error,
		RunAfter: j.RunAfter, CreatedAt: j.CreatedAt, FinishedAt: j.FinishedAt}
}

// summary is a job as lists and streams show it: a finished reconcile's
// snapshot is cut to its totals, and an export's card IDs to a count. Get the
// job for the whole of either.
func summary(j jobOut) jobOut {
	switch j.Kind {
	case targets.ReconcileKind:
		var snap struct {
			Totals map[string]int `json:"totals"`
		}
		if json.Unmarshal(j.Progress, &snap) == nil && snap.Totals != nil {
			j.Progress, _ = json.Marshal(map[string]any{"totals": snap.Totals})
		}
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
	Status string `query:"status" enum:"queued,running,waiting,succeeded,failed"`
	Kind   string `query:"kind"`
	Parent int64  `query:"parent" doc:"List this bulk job's children instead of top-level jobs"`
	Before int64  `query:"before" doc:"Only jobs with smaller IDs, for the next page"`
	Limit  int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
}

type importIn struct {
	Body struct {
		InstanceID int64           `json:"instance_id"`
		Params     json.RawMessage `json:"params,omitempty" doc:"The import plugin's settings, such as a folder path. For a target app: remote_ids, to import only those items."`
	}
}

type bulkIn struct {
	Body struct {
		Query      library.Query `json:"query" doc:"Selects the cards, as in POST /search. Paging is ignored. To pick cards by ID, filter on ids."`
		Kind       string        `json:"kind,omitempty" enum:"card,lorebook" doc:"What the query selects; default card. Lorebooks take archive and unarchive only."`
		Action     string        `json:"action" enum:"archive,unarchive,tag,untag,collect,uncollect,export"`
		Tags       []string      `json:"tags,omitempty" maxItems:"100" maxLength:"100" doc:"For tag and untag: the tags to add or remove."`
		Collection int64         `json:"collection,omitempty" doc:"For collect and uncollect: the collection."`
		Format     string        `json:"format,omitempty" doc:"For export: json (the default), or a format a codec plugin instance writes, such as png."`
	}
}

// usable checks that an instance exists, isn't broken, and its plugin has
// capability.
func (s *Server) usable(ctx context.Context, id int64, capability string) error {
	inst, err := s.Registry.GetInstance(ctx, id)
	if err != nil {
		return s.fail(err)
	}
	if inst.BrokenError != nil {
		return s.fail(plugins.ErrBroken)
	}
	if !s.Registry.HasCapability(inst.Plugin, capability) {
		return huma.Error400BadRequest(fmt.Sprintf("plugin %s can't %s", inst.Plugin, capability))
	}
	return nil
}

// importFromTarget starts a bulk import from a target app. params may name
// remote_ids to import only those items.
func (s *Server) importFromTarget(ctx context.Context, instanceID int64, params json.RawMessage) (*jobResp, error) {
	if err := s.usable(ctx, instanceID, "pull"); err != nil {
		return nil, err
	}
	var p struct {
		RemoteIDs []string `json:"remote_ids"`
	}
	if len(params) > 0 {
		dec := json.NewDecoder(bytes.NewReader(params))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&p); err != nil {
			return nil, huma.Error400BadRequest("params: " + err.Error())
		}
	}
	parent, err := s.Targets.EnqueueImport(ctx, s.Queue, instanceID, p.RemoteIDs)
	if err != nil {
		return nil, s.fail(err)
	}
	j, err := s.job(ctx, parent.ID)
	if err != nil {
		return nil, err
	}
	return &jobResp{Body: j}, nil
}

// job returns a job, with child counts and outcomes for a bulk job.
func (s *Server) job(ctx context.Context, id int64) (jobOut, error) {
	j, err := s.Queue.Get(ctx, id)
	if err != nil {
		return jobOut{}, s.fail(err)
	}
	out := toJob(j)
	if j.Kind == bulk.Kind {
		if out.Children, err = s.Queue.ChildCounts(ctx, id); err != nil {
			return jobOut{}, s.fail(err)
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
		Summary: "List jobs", Description: "Newest first. A finished reconcile's progress is cut to its totals, and an export's card_ids to a count of cards; " +
			"get the job for the whole of either.", Tags: tags},
		func(ctx context.Context, in *listJobsIn) (*jobsOut, error) {
			o := jobs.ListOptions{Status: in.Status, Kind: in.Kind, Before: in.Before, Limit: in.Limit}
			if in.Parent != 0 {
				o.Parent = &in.Parent
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
			counts, err := s.Queue.ChildCountsFor(ctx, parents)
			if err != nil {
				return nil, s.fail(err)
			}
			out := &jobsOut{Body: make([]jobOut, len(list))}
			for i, j := range list {
				out.Body[i] = summary(toJob(j))
				out.Body[i].Children = counts[j.ID]
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

	huma.Register(api, huma.Operation{OperationID: "watch-job", Method: http.MethodGet, Path: "/jobs/{id}/events",
		Summary: "Stream a job's progress",
		Description: "Server-sent events: a job event with the whole job each time it changes. The stream ends when the job " +
			"finishes. A reconcile's progress and an export's card IDs are cut as in the job list; get the job for the whole of either.",
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
		Summary: "Start an import",
		Description: "Queues an import job for an instance of an import plugin. For a target app, queues a bulk job that " +
			"takes each character and lorebook not yet bound on the instance into the library and binds it; the payload " +
			"counts those skipped as already_bound.",
		Tags: tags, DefaultStatus: http.StatusAccepted},
		func(ctx context.Context, in *importIn) (*jobResp, error) {
			inst, err := s.Registry.GetInstance(ctx, in.Body.InstanceID)
			if err != nil {
				return nil, s.fail(err)
			}
			if !s.Registry.HasCapability(inst.Plugin, "import") && s.Registry.HasCapability(inst.Plugin, "push") {
				return s.importFromTarget(ctx, in.Body.InstanceID, in.Body.Params)
			}
			if err := s.usable(ctx, in.Body.InstanceID, "import"); err != nil {
				return nil, err
			}
			params := in.Body.Params
			if params == nil {
				params = json.RawMessage(`{}`)
			}
			j, err := s.Queue.Enqueue(ctx, jobs.Spec{Kind: importer.Kind, InstanceID: &in.Body.InstanceID,
				Payload: importer.Payload{Params: params}})
			if err != nil {
				return nil, s.fail(err)
			}
			return &jobResp{Body: toJob(j)}, nil
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
				cols, err := s.Lib.Collections(ctx)
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
			search := s.Lib.SearchIDs
			if lorebooks {
				search = s.Lib.SearchLorebookIDs
			}
			ids, err := search(ctx, q)
			if err != nil {
				return nil, s.fail(err)
			}
			var p jobs.Job
			switch b.Action {
			case "archive", "unarchive":
				if lorebooks {
					p, err = bulk.EnqueueLorebookArchive(ctx, s.Queue, ids, b.Action == "archive")
				} else {
					p, err = bulk.EnqueueArchive(ctx, s.Queue, ids, b.Action == "archive")
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
