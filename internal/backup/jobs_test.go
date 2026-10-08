package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/davidgibbons/innkeeper/internal/jobs"
)

// runJob runs one job through a real worker and returns it once it ends.
func runJob(t *testing.T, pool *pgxpool.Pool, kind string, h jobs.Handler, payload any) jobs.Job {
	t.Helper()
	q := jobs.New(pool)
	w := &jobs.Worker{Queue: q, Poll: 50 * time.Millisecond, Handlers: map[string]jobs.Handler{kind: h},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	defer func() { cancel(); <-done }()
	j, err := q.Enqueue(ctx, jobs.Spec{Kind: kind, Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if j, err = q.Get(ctx, j.ID); err != nil {
			t.Fatal(err)
		}
		if j.Status == jobs.StatusSucceeded || j.Status == jobs.StatusFailed {
			return j
		}
	}
	t.Fatalf("job still %s", j.Status)
	return j
}

func jobError(j jobs.Job) string {
	if j.Error == nil {
		return ""
	}
	return *j.Error
}

func TestBackupHandlerPrunesToKeep(t *testing.T) {
	needPgDump(t)
	pool, _, o := setup(t)
	s := Settings{Dir: o.Dir, Keep: map[Type]int{Config: 1}}
	h := BackupHandler(s, pool, Options{DatabaseURL: o.DatabaseURL, Store: o.Store}, Config)
	var j jobs.Job
	for range 2 {
		if j = runJob(t, pool, Kind(Config), h, nil); j.Status != jobs.StatusSucceeded {
			t.Fatalf("job %s: %s", j.Status, jobError(j))
		}
	}
	infos, err := List(o.Dir)
	if err != nil || len(infos) != 1 {
		t.Fatalf("List = %v, %v; want one backup", infos, err)
	}
	var p struct{ Name string }
	if err := json.Unmarshal(j.Progress, &p); err != nil || p.Name != infos[0].Name {
		t.Fatalf("progress %s, want name %q", j.Progress, infos[0].Name)
	}
}

func TestBackupHandlerSucceedsWhenPruneFails(t *testing.T) {
	pool, _, o := setup(t)
	// A damaged backup stops the shared-blob sweep, which prune reports.
	if err := os.MkdirAll(filepath.Join(o.Dir, "assets", "assets-20000101T000000.000000Z"), 0o700); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	o.Log = slog.New(slog.NewTextHandler(&out, nil))
	h := BackupHandler(Settings{Dir: o.Dir, Keep: map[Type]int{Assets: 1}}, pool, o, Assets)
	if j := runJob(t, pool, Kind(Assets), h, nil); j.Status != jobs.StatusSucceeded {
		t.Fatalf("job %s: %s", j.Status, jobError(j))
	}
	if !strings.Contains(out.String(), "not pruned") || !strings.Contains(out.String(), "assets-20000101T000000.000000Z") {
		t.Fatalf("log = %q", out.String())
	}
}

func TestSweepUploads(t *testing.T) {
	pool, _, o := setup(t)
	ctx := context.Background()
	if n, err := SweepUploads(ctx, pool, o.Dir, time.Hour); n != 0 || err != nil {
		t.Fatalf("no restore folder: %d, %v", n, err)
	}
	rd := RestoreDir(o.Dir)
	if err := os.Mkdir(rd, 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	files := map[string]time.Time{"queued": old, "orphan": old, "fresh": time.Now()}
	for name, mtime := range files {
		p := filepath.Join(rd, name)
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := jobs.New(pool).Enqueue(ctx, jobs.Spec{Kind: RestoreKind, Payload: RestorePayload{Path: filepath.Join(rd, "queued")}}); err != nil {
		t.Fatal(err)
	}
	if n, err := SweepUploads(ctx, pool, o.Dir, time.Hour); n != 1 || err != nil {
		t.Fatalf("SweepUploads = %d, %v; want 1", n, err)
	}
	for name := range files {
		if _, err := os.Stat(filepath.Join(rd, name)); (name == "orphan") != os.IsNotExist(err) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestBackupHandlerNeedsDir(t *testing.T) {
	pool, _, o := setup(t)
	j := runJob(t, pool, Kind(Assets), BackupHandler(Settings{}, pool, o, Assets), nil)
	if j.Status != jobs.StatusFailed || !strings.Contains(jobError(j), "INNKEEPER_BACKUP_DIR") || j.Attempts != 1 {
		t.Fatalf("job %s after %d attempts: %q", j.Status, j.Attempts, jobError(j))
	}
}

func TestBackupHandlerRetriesFailedWrite(t *testing.T) {
	pool, _, o := setup(t)
	notDir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notDir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	err := BackupHandler(Settings{Dir: notDir}, pool, o, Assets)(context.Background(), &jobs.Run{})
	if !errors.Is(err, jobs.ErrRetry) {
		t.Fatalf("err = %v, want ErrRetry", err)
	}
}

func TestRestoreHandler(t *testing.T) {
	assets, _, _ := source(t)
	tg := newTarget(t)
	dir := t.TempDir()
	if err := os.Mkdir(RestoreDir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	s := Settings{Dir: dir}
	h := RestoreHandler(s, RestoreOptions{Conn: tg.pool.Config().ConnConfig, Store: tg.store}, nil)

	upload := filepath.Join(RestoreDir(dir), "up.tar.gz")
	if err := os.WriteFile(upload, assets, 0o600); err != nil {
		t.Fatal(err)
	}
	j := runJob(t, tg.pool, RestoreKind, h, RestorePayload{Path: upload})
	if j.Status != jobs.StatusSucceeded {
		t.Fatalf("job %s: %s", j.Status, jobError(j))
	}
	checkAssets(t, tg)
	if _, err := os.Stat(upload); !os.IsNotExist(err) {
		t.Fatalf("upload not removed: %v", err)
	}

	// A failed restore (the data is there now) removes the upload too.
	if err := os.WriteFile(upload, assets, 0o600); err != nil {
		t.Fatal(err)
	}
	if j = runJob(t, tg.pool, RestoreKind, h, RestorePayload{Path: upload}); j.Status != jobs.StatusFailed {
		t.Fatalf("job %s, want failed", j.Status)
	}
	if _, err := os.Stat(upload); !os.IsNotExist(err) {
		t.Fatalf("upload kept after failure: %v", err)
	}
}

func TestRestoreHandlerRejectsOutsidePaths(t *testing.T) {
	tg := newTarget(t)
	dir := t.TempDir()
	if err := os.Mkdir(RestoreDir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(t.TempDir(), "x.tar.gz")
	secret := filepath.Join(dir, "x.tar.gz")
	for _, p := range []string{elsewhere, secret, RestoreDir(dir) + "/../x.tar.gz", RestoreDir(dir), RestoreDir(dir) + "/sub/x.tar.gz"} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil && !os.IsNotExist(err) && !strings.Contains(err.Error(), "directory") {
			t.Fatal(err)
		}
	}
	h := RestoreHandler(Settings{Dir: dir}, RestoreOptions{Conn: tg.pool.Config().ConnConfig, Store: tg.store}, nil)
	for _, p := range []string{elsewhere, "../x.tar.gz", secret, RestoreDir(dir) + "/../x.tar.gz", RestoreDir(dir) + "/sub/x.tar.gz"} {
		j := runJob(t, tg.pool, RestoreKind, h, RestorePayload{Path: p})
		if j.Status != jobs.StatusFailed || !strings.Contains(jobError(j), "not a file in") {
			t.Errorf("%s: job %s: %q", p, j.Status, jobError(j))
		}
	}
	for _, p := range []string{elsewhere, secret} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("rejected path was touched: %v", err)
		}
	}
}
