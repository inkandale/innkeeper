package backup

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strings"
)

// pgDumpCommand runs pg_dump on dbURL with args, writing to out. The password
// goes in the environment, since other users can see a process's arguments.
func pgDumpCommand(dbURL string, out io.Writer, args ...string) (*exec.Cmd, error) {
	u, err := url.Parse(dbURL)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return nil, errors.New("backup needs INNKEEPER_DATABASE_URL as a postgres:// URL")
	}
	env := os.Environ()
	pw, ok := "", false
	if u.User != nil {
		pw, ok = u.User.Password()
		u.User = url.User(u.User.Username())
	}
	if q := u.Query(); q.Has("password") {
		pw, ok = q.Get("password"), true
		q.Del("password")
		u.RawQuery = q.Encode()
	}
	if ok {
		env = append(env, "PGPASSWORD="+pw)
	}
	cmd := exec.Command("pg_dump", append([]string{"--dbname=" + u.String()}, args...)...)
	cmd.Env, cmd.Stdout = env, out
	return cmd, nil
}

// runPgDump runs cmd and puts pg_dump's stderr in the error, so a failed job
// says why.
func runPgDump(cmd *exec.Cmd) error {
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("pg_dump: %w: %s", err, msg)
		}
		return fmt.Errorf("pg_dump: %w", err)
	}
	return nil
}
