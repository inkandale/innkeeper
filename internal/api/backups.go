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
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

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

// restoreBackupIn has no RawBody, so huma leaves the body unread: its
// multipart parsing would spool the archive to TMPDIR. The handler streams it
// to the backup volume instead.
type restoreBackupIn struct{ req *http.Request }

func (in *restoreBackupIn) Resolve(hc huma.Context) []error {
	in.req, _ = humago.Unwrap(hc)
	return nil
}

// restoreBody documents what the start-restore handler reads itself.
var restoreBody = &huma.RequestBody{Content: map[string]*huma.MediaType{"multipart/form-data": {
	Schema: &huma.Schema{Type: "object", Required: []string{"archive"}, Properties: map[string]*huma.Schema{
		"archive": {Type: "string", Format: "binary", ContentEncoding: "binary",
			Description: "A backup archive, as GET /backups/{name} returns."},
		"replace": {Type: "boolean", Description: "Replace data of the archive's type instead of refusing when some exists."},
	}},
	Encoding: map[string]*huma.Encoding{"archive": {ContentType: "application/octet-stream"}, "replace": {ContentType: "text/plain"}},
}}}

// readRestoreForm saves the archive part to dir's restore folder as it
// arrives and returns its path and the replace field.
func readRestoreForm(dir string, r *http.Request) (path string, replace bool, err error) {
	defer func() {
		if err != nil && path != "" {
			os.Remove(path)
			path = ""
		}
	}()
	mr, err := r.MultipartReader()
	if err != nil {
		return "", false, huma.Error400BadRequest(err.Error())
	}
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return path, false, huma.Error400BadRequest("reading form: " + err.Error())
		}
		switch p.FormName() {
		case "archive":
			if path != "" {
				return path, false, huma.Error400BadRequest("archive given twice")
			}
			if path, err = saveUpload(dir, p); err != nil {
				return "", false, err
			}
		case "replace":
			b, err := io.ReadAll(io.LimitReader(p, 16))
			if err != nil {
				return path, false, huma.Error400BadRequest("reading form: " + err.Error())
			}
			if replace, err = strconv.ParseBool(string(b)); err != nil {
				return path, false, huma.Error400BadRequest("replace must be true or false")
			}
		}
	}
	if path == "" {
		return "", false, huma.Error400BadRequest("archive is required")
	}
	return path, replace, nil
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

// readManifest checks that the archive at path starts with a valid manifest.
func readManifest(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = backup.ReadManifest(f)
	return err
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
			return s.enqueue(ctx, jobs.Spec{Kind: backup.Kind(in.Body.Type)})
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
		Tags: tags, DefaultStatus: http.StatusAccepted},
		func(ctx context.Context, in *restoreBackupIn) (*jobResp, error) {
			dir, err := s.backupDir()
			if err != nil {
				return nil, err
			}
			path, replace, err := readRestoreForm(dir, in.req)
			if err != nil {
				var se huma.StatusError
				if errors.As(err, &se) {
					return nil, err
				}
				return nil, s.fail(err)
			}
			if err := readManifest(path); err != nil {
				os.Remove(path)
				return nil, huma.Error400BadRequest(err.Error())
			}
			j, err := s.Queue.Enqueue(ctx, jobs.Spec{Kind: backup.RestoreKind,
				Payload: backup.RestorePayload{Path: path, Replace: replace}})
			if err != nil {
				os.Remove(path)
				return nil, s.fail(err)
			}
			return &jobResp{Body: toJob(j)}, nil
		})
	// Set after registering, since a schema here would make huma read the body.
	api.OpenAPI().Paths["/restores"].Post.RequestBody = restoreBody

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
