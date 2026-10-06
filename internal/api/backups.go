package api

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"reflect"
	"slices"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/davidgibbons/innkeeper/internal/backup"
	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/tier"
)

type backupOut struct {
	Name      string      `json:"name,omitempty" doc:"Set once the backup is complete; download it by this name."`
	Type      backup.Type `json:"type" enum:"assets,config"`
	CreatedAt time.Time   `json:"created_at"`
	Size      int64       `json:"size,omitempty"`
	Status    string      `json:"status" enum:"complete,queued,running,failed"`
	JobID     *int64      `json:"job_id,omitempty"`
	Error     *string     `json:"error,omitempty"`
}

type backupsOut struct{ Body []backupOut }

type startBackupIn struct {
	Body struct {
		Type backup.Type `json:"type" enum:"assets,config"`
	}
}

type backupNameIn struct {
	Name string `path:"name"`
}

type restoreBackupIn struct {
	RawBody huma.MultipartFormFiles[struct {
		Archive huma.FormFile `form:"archive" required:"true" doc:"A backup archive, as GET /backups/{name} returns."`
		Replace bool          `form:"replace" required:"false" doc:"Replace data of the archive's type instead of refusing when some exists."`
	}]
}

type typeHealth struct {
	Every string     `json:"every"`
	Last  *time.Time `json:"last"`
	OK    bool       `json:"ok"`
}

type health struct {
	Backups map[backup.Type]typeHealth `json:"backups"`
}

type healthOut struct {
	Status int
	Body   health
}

// backupDir is where backups live, or a 409 when backups are off.
func (s *Server) backupDir() (string, error) {
	if s.Backups.Dir == "" {
		return "", huma.Error409Conflict("backups are off; set INNKEEPER_BACKUP_DIR")
	}
	return s.Backups.Dir, nil
}

// listBackups merges finished backups with backup jobs not yet reflected in
// them: those queued, running, or failed since their type's latest backup.
func (s *Server) listBackups(ctx context.Context, dir string) ([]backupOut, error) {
	infos, err := backup.List(dir)
	if err != nil {
		return nil, s.fail(err)
	}
	latest := map[backup.Type]time.Time{}
	var out []backupOut
	for _, i := range infos {
		if latest[i.Type].IsZero() {
			latest[i.Type] = i.CreatedAt
		}
		out = append(out, backupOut{Name: i.Name, Type: i.Type, CreatedAt: i.CreatedAt, Size: i.Size, Status: "complete"})
	}
	for _, t := range backup.Types {
		// ponytail: only the newest 50 jobs per type; page with Before if failures pile up past that.
		list, err := s.Queue.List(ctx, jobs.ListOptions{Kind: backup.Kind(t), Limit: 50})
		if err != nil {
			return nil, s.fail(err)
		}
		for _, j := range list {
			if !j.CreatedAt.After(latest[t]) {
				break
			}
			switch j.Status {
			case jobs.StatusQueued, jobs.StatusRunning, jobs.StatusFailed:
				out = append(out, backupOut{Type: t, CreatedAt: j.CreatedAt, Status: j.Status, JobID: &j.ID, Error: j.Error})
			}
		}
	}
	slices.SortStableFunc(out, func(a, b backupOut) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return out, nil
}

// lazyWriter sends the response's headers with its first byte, so a backup
// that fails its checks can still get an error status.
type lazyWriter struct {
	hc      huma.Context
	start   func()
	started bool
}

func (w *lazyWriter) Write(p []byte) (int, error) {
	if !w.started {
		w.started = true
		w.start()
	}
	return w.hc.BodyWriter().Write(p)
}

// saveUpload copies an uploaded archive into dir's restore folder for the
// restore job, and returns its path.
func saveUpload(dir string, r io.Reader) (string, error) {
	rd := backup.RestoreDir(dir)
	if err := os.MkdirAll(rd, 0o700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(rd, "upload-*.tar.gz")
	if err != nil {
		return "", err
	}
	_, err = io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

func (s *Server) backupRoutes(api huma.API) {
	tags := []string{"Backups"}
	// Assets backups hold every card and config backups the encrypted secrets.
	destructive := needs(tier.Destructive)

	huma.Register(api, huma.Operation{Metadata: destructive, OperationID: "list-backups", Method: http.MethodGet, Path: "/backups",
		Summary:     "List backups",
		Description: "Newest first: finished backups, plus backup jobs queued, running, or failed since their type's latest backup.",
		Tags:        tags},
		func(ctx context.Context, _ *struct{}) (*backupsOut, error) {
			dir, err := s.backupDir()
			if err != nil {
				return nil, err
			}
			out, err := s.listBackups(ctx, dir)
			if err != nil {
				return nil, err
			}
			return &backupsOut{Body: orEmpty(out)}, nil
		})

	huma.Register(api, huma.Operation{Metadata: destructive, OperationID: "start-backup", Method: http.MethodPost, Path: "/backups",
		Summary: "Start a backup", Description: "Queues a backup job. Its progress names the backup when it's done.",
		Tags: tags, DefaultStatus: http.StatusAccepted},
		func(ctx context.Context, in *startBackupIn) (*jobResp, error) {
			if _, err := s.backupDir(); err != nil {
				return nil, err
			}
			j, err := s.Queue.Enqueue(ctx, jobs.Spec{Kind: backup.Kind(in.Body.Type)})
			if err != nil {
				return nil, s.fail(err)
			}
			return &jobResp{Body: toJob(j)}, nil
		})

	huma.Register(api, huma.Operation{Metadata: destructive, OperationID: "download-backup", Method: http.MethodGet, Path: "/backups/{name}",
		Summary: "Download a backup", Description: "A gzip tar archive, which POST /restores takes.", Tags: tags,
		Responses: map[string]*huma.Response{"200": {Description: "The backup archive",
			Content: map[string]*huma.MediaType{"application/gzip": {}}}}},
		func(ctx context.Context, in *backupNameIn) (*huma.StreamResponse, error) {
			dir, err := s.backupDir()
			if err != nil {
				return nil, err
			}
			// WriteArchive rejects names that aren't plain backup folders before touching the filesystem.
			return &huma.StreamResponse{Body: func(hc huma.Context) {
				w := &lazyWriter{hc: hc, start: func() {
					hc.SetHeader("Content-Type", "application/gzip")
					hc.SetHeader("Content-Disposition", `attachment; filename="`+in.Name+`.tar.gz"`)
				}}
				err := backup.WriteArchive(w, dir, in.Name)
				switch {
				case err == nil:
				case w.started:
					s.Log.Warn("send backup", "name", in.Name, "err", err)
					// Aborting breaks the transfer, so the client can't mistake a truncated archive for a whole one.
					panic(http.ErrAbortHandler)
				case errors.Is(err, fs.ErrNotExist):
					huma.WriteErr(api, hc, http.StatusNotFound, "not found")
				default:
					s.Log.Error("backup is damaged", "name", in.Name, "err", err)
					huma.WriteErr(api, hc, http.StatusInternalServerError, "backup is damaged; see the server log")
				}
			}}, nil
		})

	huma.Register(api, huma.Operation{Metadata: destructive, OperationID: "start-restore", Method: http.MethodPost, Path: "/restores",
		Summary: "Restore a backup",
		Description: "Uploads a backup archive and queues a job that restores it. Without replace, the job fails if the " +
			"database already has data of the archive's type.",
		Tags: tags, DefaultStatus: http.StatusAccepted,
		// Archives can be many GB; multipart uploads spool to disk, not memory.
		MaxBodyBytes: -1, BodyReadTimeout: -1},
		func(ctx context.Context, in *restoreBackupIn) (*jobResp, error) {
			dir, err := s.backupDir()
			if err != nil {
				return nil, err
			}
			f := in.RawBody.Data().Archive
			if _, err := backup.ReadManifest(f); err != nil {
				return nil, huma.Error400BadRequest(err.Error())
			}
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				return nil, s.fail(err)
			}
			path, err := saveUpload(dir, f)
			if err != nil {
				return nil, s.fail(err)
			}
			j, err := s.Queue.Enqueue(ctx, jobs.Spec{Kind: backup.RestoreKind,
				Payload: backup.RestorePayload{Path: path, Replace: in.RawBody.Data().Replace}})
			if err != nil {
				os.Remove(path)
				return nil, s.fail(err)
			}
			return &jobResp{Body: toJob(j)}, nil
		})

	healthSchema := api.OpenAPI().Components.Schemas.Schema(reflect.TypeFor[health](), true, "Health")
	huma.Register(api, huma.Operation{Metadata: public, Security: noAuth, OperationID: "health", Method: http.MethodGet, Path: "/health",
		Summary: "Check health",
		Description: "Each scheduled backup type is ok when its latest backup is younger than twice its schedule. " +
			"Any type not ok returns 503.",
		Tags: []string{"Health"},
		Responses: map[string]*huma.Response{"503": {Description: "A scheduled backup is overdue",
			Content: map[string]*huma.MediaType{"application/json": {Schema: healthSchema}}}}},
		func(ctx context.Context, _ *struct{}) (*healthOut, error) {
			out := &healthOut{Status: http.StatusOK, Body: health{Backups: map[backup.Type]typeHealth{}}}
			if len(s.Backups.Every) == 0 {
				return out, nil
			}
			latest := map[backup.Type]time.Time{}
			if s.Backups.Dir != "" {
				var err error
				if latest, err = backup.Latest(s.Backups.Dir); err != nil {
					return nil, s.fail(err)
				}
			}
			for t, every := range s.Backups.Every {
				h := typeHealth{Every: every.String()}
				if at, ok := latest[t]; ok {
					h.Last = &at
					h.OK = time.Since(at) < 2*every
				}
				if !h.OK {
					out.Status = http.StatusServiceUnavailable
				}
				out.Body.Backups[t] = h
			}
			return out, nil
		})
}
