package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/davidgibbons/innkeeper/internal/bulk"
	"github.com/davidgibbons/innkeeper/internal/importer"
	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/library"
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
	Progress    json.RawMessage `json:"progress" doc:"An import's has done, total, message, imported, skipped, failed, and errors."`
	ParentID    *int64          `json:"parent_id,omitempty"`
	Error       *string         `json:"error,omitempty"`
	RunAfter    time.Time       `json:"run_after"`
	CreatedAt   time.Time       `json:"created_at"`
	FinishedAt  *time.Time      `json:"finished_at,omitempty"`
	Children    map[string]int  `json:"children,omitempty" doc:"A bulk job's children, counted by status. Only get-job, cancel-job, and watch-job fill it."`
}

func toJob(j jobs.Job) jobOut {
	return jobOut{ID: j.ID, Kind: j.Kind, InstanceID: j.InstanceID, Payload: j.Payload, Status: j.Status,
		Attempts: j.Attempts, MaxAttempts: j.MaxAttempts, Progress: j.Progress, ParentID: j.ParentID, Error: j.Error,
		RunAfter: j.RunAfter, CreatedAt: j.CreatedAt, FinishedAt: j.FinishedAt}
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
		Params     json.RawMessage `json:"params,omitempty" doc:"The import plugin's settings, such as a folder path"`
	}
}

type bulkIn struct {
	Body struct {
		Query  library.Query `json:"query" doc:"Selects the cards, as in POST /search. Paging is ignored."`
		Action string        `json:"action" enum:"archive,unarchive"`
	}
}

// job returns a job, with child counts for a bulk job.
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
		data, err := json.Marshal(j)
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
		Summary: "List jobs", Description: "Newest first.", Tags: tags},
		func(ctx context.Context, in *listJobsIn) (*jobsOut, error) {
			o := jobs.ListOptions{Status: in.Status, Kind: in.Kind, Before: in.Before, Limit: in.Limit}
			if in.Parent != 0 {
				o.Parent = &in.Parent
			}
			list, err := s.Queue.List(ctx, o)
			if err != nil {
				return nil, s.fail(err)
			}
			out := &jobsOut{Body: make([]jobOut, len(list))}
			for i, j := range list {
				out.Body[i] = toJob(j)
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

	huma.Register(api, huma.Operation{OperationID: "cancel-job", Method: http.MethodPost, Path: "/jobs/{id}/cancel",
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
		Summary:     "Stream a job's progress",
		Description: "Server-sent events: a job event with the whole job each time it changes. The stream ends when the job finishes.",
		Tags:        tags,
		Responses: map[string]*huma.Response{"200": {Description: "A stream of job events",
			Content: map[string]*huma.MediaType{"text/event-stream": {}}}}},
		func(ctx context.Context, in *idIn) (*huma.StreamResponse, error) {
			first, err := s.job(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			return &huma.StreamResponse{Body: func(hc huma.Context) { s.watch(hc, first) }}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "start-import", Method: http.MethodPost, Path: "/imports",
		Summary: "Start an import", Description: "Queues an import job for an instance of an import plugin.",
		Tags: tags, DefaultStatus: http.StatusAccepted},
		func(ctx context.Context, in *importIn) (*jobResp, error) {
			inst, err := s.Registry.GetInstance(ctx, in.Body.InstanceID)
			if err != nil {
				return nil, s.fail(err)
			}
			// The stored error can hold paths and plugin output, so it stays out of the response.
			if inst.BrokenError != nil {
				return nil, huma.Error409Conflict("plugin instance is broken; fix its settings first")
			}
			if m := s.Registry.Manifests()[inst.Plugin]; m == nil || !slices.Contains(m.Capabilities, "import") {
				return nil, huma.Error400BadRequest(fmt.Sprintf("plugin %s can't import", inst.Plugin))
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

	huma.Register(api, huma.Operation{OperationID: "start-bulk", Method: http.MethodPost, Path: "/bulk",
		Summary: "Archive or unarchive many cards", Description: "Queues one child job per card the query matches.",
		Tags: tags, DefaultStatus: http.StatusAccepted},
		func(ctx context.Context, in *bulkIn) (*jobResp, error) {
			q := in.Body.Query
			if q.Filter == nil && q.Text == "" {
				return nil, huma.Error400BadRequest("bulk actions need a filter or text; to select every card, filter on archived")
			}
			// Select only cards the action changes; search would otherwise hide archived cards from unarchive.
			archive := in.Body.Action == "archive"
			wasArchived := !archive
			state := library.Filter{Archived: &wasArchived}
			if q.Filter != nil {
				state = library.Filter{And: []library.Filter{*q.Filter, state}}
			}
			q.Filter = &state
			ids, err := s.Lib.SearchIDs(ctx, q)
			if err != nil {
				return nil, s.fail(err)
			}
			p, err := bulk.EnqueueArchive(ctx, s.Queue, ids, archive)
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
