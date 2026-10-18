package jobs

import (
	"context"
	"slices"
	"time"
)

// maxLogLines is how many log lines a job keeps; older ones are dropped.
const maxLogLines = 1000

// maxLogMessage is the longest log message kept, in bytes.
const maxLogMessage = 4096

// LogLine is one line of a job's log.
type LogLine struct {
	ID      int64     `json:"id"`
	At      time.Time `json:"at"`
	Level   string    `json:"level" enum:"info,warn,error"`
	Message string    `json:"message"`
}

// AppendLog adds a line to the job's log, dropping its oldest lines past
// maxLogLines. An unknown level is logged as info.
func (q *Queue) AppendLog(ctx context.Context, job int64, level, message string) error {
	if !slices.Contains([]string{"info", "warn", "error"}, level) {
		level = "info"
	}
	if len(message) > maxLogMessage {
		message = message[:maxLogMessage]
	}
	// The DELETE sees the log as it was before the INSERT, so it keeps one line fewer.
	_, err := q.pool.Exec(ctx, `WITH added AS (INSERT INTO job_log (job_id, level, message) VALUES ($1, $2, $3))
		DELETE FROM job_log WHERE job_id = $1
			AND id <= (SELECT id FROM job_log WHERE job_id = $1 ORDER BY id DESC OFFSET $4 LIMIT 1)`,
		job, level, message, maxLogLines-1)
	return err
}

// Logs returns the job's log lines after the line with ID after, oldest
// first.
func (q *Queue) Logs(ctx context.Context, job, after int64, limit int) ([]LogLine, error) {
	rows, err := q.pool.Query(ctx, `SELECT id, at, level, message FROM job_log
		WHERE job_id = $1 AND id > $2 ORDER BY id LIMIT $3`, job, after, limit)
	if err != nil {
		return nil, err
	}
	out := []LogLine{}
	for rows.Next() {
		var l LogLine
		if err := rows.Scan(&l.ID, &l.At, &l.Level, &l.Message); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
