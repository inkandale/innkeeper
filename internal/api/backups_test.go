package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/davidgibbons/innkeeper/internal/backup"
	"github.com/davidgibbons/innkeeper/internal/tier"
)

// writeBackup hand-builds a finished backup with no tables and returns its name.
func writeBackup(t *testing.T, dir string, typ backup.Type, created time.Time) string {
	t.Helper()
	name := string(typ) + "-" + created.UTC().Format("20060102T150405.000000Z")
	folder := filepath.Join(dir, string(typ), name)
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	m, err := json.Marshal(backup.Manifest{Format: 1, Type: typ, CreatedAt: created, Tables: map[string]int64{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "manifest.json"), m, 0o600); err != nil {
		t.Fatal(err)
	}
	return name
}

// upload posts archive to /restores as multipart, with a destructive token.
func (e *testEnv) upload(archive []byte, replace string) result {
	e.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("archive", "backup.tar.gz")
	if err != nil {
		e.t.Fatal(err)
	}
	fw.Write(archive)
	if replace != "" {
		mw.WriteField("replace", replace)
	}
	mw.Close()
	req, err := http.NewRequest("POST", e.srv.URL+"/api/restores", &body)
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+e.token)
	resp, err := client.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return result{resp.StatusCode, resp.Header, raw}
}

func TestBackupRoutesNeedDestructiveAndDir(t *testing.T) {
	e := newEnv(t)
	write := e.tokenWith(tier.Write)
	routes := [][2]string{{"GET", "/backups"}, {"POST", "/backups"}, {"GET", "/backups/x"}, {"POST", "/restores"}}
	for _, r := range routes {
		if got := e.send(r[0], r[1], nil, "Authorization", "Bearer "+write); got.Status != http.StatusForbidden {
			t.Errorf("%s %s with a write token: %s", r[0], r[1], got)
		}
	}
	e.call("GET", "/backups", nil, 409, nil)
	r := e.call("POST", "/backups", map[string]any{"type": "assets"}, 409, nil)
	if !strings.Contains(string(r.Body), "INNKEEPER_BACKUP_DIR") {
		t.Errorf("409 doesn't name INNKEEPER_BACKUP_DIR: %s", r)
	}
	e.call("GET", "/backups/x", nil, 409, nil)
	if r := e.upload([]byte("x"), ""); r.Status != 409 {
		t.Errorf("restore without a dir: %s", r)
	}
	// Nothing scheduled needs no dir and no token.
	var h struct{ Backups map[string]any }
	if r := e.send("GET", "/health", nil); r.Status != 200 || json.Unmarshal(r.Body, &h) != nil || h.Backups == nil || len(h.Backups) != 0 {
		t.Errorf("health with nothing scheduled: %s", r)
	}
}

func TestBackups(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	dir := t.TempDir()
	e.s.Backups = backup.Settings{Dir: dir}

	var j jobOut
	e.call("POST", "/backups", map[string]any{"type": "assets"}, 202, &j)
	if j.Kind != "backup.assets" || j.Status != "queued" {
		t.Fatalf("started %+v", j)
	}
	e.call("POST", "/backups", map[string]any{"type": "nope"}, 422, nil)
	// Only failures count from here on.
	if err := e.s.Queue.Cancel(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE job SET created_at = now() - interval '1 day' WHERE id = $1`, j.ID); err != nil {
		t.Fatal(err)
	}

	created := time.Now().Add(-time.Hour).UTC().Truncate(time.Microsecond)
	name := writeBackup(t, dir, backup.Config, created)
	writeBackup(t, dir, backup.Assets, created.Add(-time.Minute))
	// An older failed config job is covered by the backup; a newer one isn't.
	var old, failed jobOut
	e.call("POST", "/backups", map[string]any{"type": "config"}, 202, &old)
	e.call("POST", "/backups", map[string]any{"type": "config"}, 202, &failed)
	for _, id := range []int64{old.ID, failed.ID} {
		if err := e.s.Queue.Cancel(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.pool.Exec(ctx, `UPDATE job SET created_at = now() - interval '2 hours' WHERE id = $1`, old.ID); err != nil {
		t.Fatal(err)
	}

	var list []backupOut
	e.call("GET", "/backups", nil, 200, &list)
	if len(list) != 3 {
		t.Fatalf("list: %+v", list)
	}
	if l := list[0]; l.Status != "failed" || l.JobID == nil || *l.JobID != failed.ID || l.Error == nil || l.Name != "" {
		t.Errorf("first: %+v", l)
	}
	if l := list[1]; l.Status != "complete" || l.Name != name || l.Type != backup.Config || l.Size == 0 {
		t.Errorf("second: %+v", l)
	}
	if l := list[2]; l.Status != "complete" || l.Type != backup.Assets {
		t.Errorf("third: %+v", l)
	}

	e.call("GET", "/backups/..%2Fx", nil, 404, nil)
	e.call("GET", "/backups/config-nope", nil, 404, nil)
	r := e.call("GET", "/backups/"+name, nil, 200, nil)
	if r.Header.Get("Content-Type") != "application/gzip" ||
		r.Header.Get("Content-Disposition") != `attachment; filename="`+name+`.tar.gz"` {
		t.Errorf("headers: %v", r.Header)
	}
	m, err := backup.ReadManifest(bytes.NewReader(r.Body))
	if err != nil || m.Type != backup.Config || !m.CreatedAt.Equal(created) {
		t.Fatalf("downloaded manifest %+v, %v", m, err)
	}

	// A listed table file gone fails before the first byte, so nothing partial goes out.
	damaged := writeBackup(t, dir, backup.Config, created.Add(-2*time.Hour))
	mpath := filepath.Join(dir, "config", damaged, "manifest.json")
	if err := os.WriteFile(mpath, []byte(`{"format":1,"type":"config","tables":{"setting":1}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if d := e.call("GET", "/backups/"+damaged, nil, 500, nil); d.Header.Get("Content-Type") == "application/gzip" ||
		!strings.Contains(string(d.Body), "damaged") {
		t.Errorf("damaged download: %s %v", d, d.Header)
	}

	if r := e.upload([]byte("not an archive"), ""); r.Status != 400 {
		t.Errorf("restore of junk: %s", r)
	}
	up := e.upload(r.Body, "true")
	if up.Status != 202 {
		t.Fatalf("restore: %s", up)
	}
	if err := json.Unmarshal(up.Body, &j); err != nil {
		t.Fatal(err)
	}
	var p backup.RestorePayload
	if err := json.Unmarshal(j.Payload, &p); err != nil {
		t.Fatal(err)
	}
	if j.Kind != backup.RestoreKind || !p.Replace || filepath.Dir(p.Path) != backup.RestoreDir(dir) {
		t.Fatalf("restore job %s %+v", j.Kind, p)
	}
	if saved, err := os.ReadFile(p.Path); err != nil || !bytes.Equal(saved, r.Body) {
		t.Errorf("saved upload differs: %v", err)
	}
}

func TestRestoreUploadNeedsArchive(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	e.s.Backups = backup.Settings{Dir: dir}
	for name, body := range map[string]string{
		"no archive":  "--b\r\nContent-Disposition: form-data; name=\"replace\"\r\n\r\ntrue\r\n--b--\r\n",
		"bad replace": "--b\r\nContent-Disposition: form-data; name=\"replace\"\r\n\r\nmaybe\r\n--b--\r\n",
	} {
		r := e.send("POST", "/restores", []byte(body), "Content-Type", "multipart/form-data; boundary=b",
			"Authorization", "Bearer "+e.token)
		if r.Status != 400 {
			t.Errorf("%s: %s", name, r)
		}
	}
	if es, _ := os.ReadDir(backup.RestoreDir(dir)); len(es) != 0 {
		t.Errorf("rejected uploads left %v", es)
	}
}

func TestDownloadAbortsPartway(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	e.s.Backups = backup.Settings{Dir: dir}
	name := writeBackup(t, dir, backup.Config, time.Now())
	folder := filepath.Join(dir, "config", name)
	// Random padding compresses poorly, so the status and some bytes go out before the failure.
	pad := make([]byte, 256<<10)
	rand.Read(pad)
	manifest := fmt.Sprintf(`{"format":1,"type":"config","tables":{"setting":1},"pad":"%x"}`, pad)
	if err := os.WriteFile(filepath.Join(folder, "manifest.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	// Present for the up-front check, unreadable once the archive has started.
	table := filepath.Join(folder, "tables", "setting.copy")
	if err := os.MkdirAll(filepath.Dir(table), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(table, []byte("x"), 0o000); err != nil {
		t.Fatal(err)
	}
	if f, err := os.Open(table); err == nil {
		f.Close()
		t.Skip("running as a user who can read mode 000 files")
	}
	req, err := http.NewRequest("GET", e.srv.URL+"/api/backups/"+name, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+e.token)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if _, err := io.ReadAll(resp.Body); err == nil {
		t.Fatal("truncated download read as complete")
	}
}

func TestHealth(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	e.s.Backups = backup.Settings{Dir: dir, Every: map[backup.Type]time.Duration{backup.Assets: time.Hour}}
	var out health
	check := func(want int) {
		t.Helper()
		r := e.send("GET", "/health", nil)
		if r.Status != want {
			t.Fatalf("health: got %s, want %d", r, want)
		}
		out = health{}
		if err := json.Unmarshal(r.Body, &out); err != nil {
			t.Fatal(err)
		}
	}
	check(503)
	if h := out.Backups[backup.Assets]; h.OK || h.Last != nil || h.Every != "1h0m0s" || len(out.Backups) != 1 {
		t.Errorf("with none: %+v", out)
	}
	writeBackup(t, dir, backup.Assets, time.Now().Add(-3*time.Hour))
	check(503)
	writeBackup(t, dir, backup.Assets, time.Now().Add(-90*time.Minute))
	check(200)
	if h := out.Backups[backup.Assets]; !h.OK || h.Last == nil {
		t.Errorf("fresh: %+v", out)
	}
	// A schedule without a dir can't have backups.
	e.s.Backups.Dir = ""
	check(503)
}
