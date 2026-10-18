package jobs

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestJobLogKeepsTheLastLines(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	j, _ := q.Enqueue(ctx, Spec{Kind: "test"})
	for i := range maxLogLines + 5 {
		if err := q.AppendLog(ctx, j.ID, "info", fmt.Sprintf("line %d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := q.AppendLog(ctx, j.ID, "loud", strings.Repeat("x", maxLogMessage+10)); err != nil {
		t.Fatal(err)
	}
	lines, err := q.Logs(ctx, j.ID, 0, 2*maxLogLines)
	if err != nil {
		t.Fatal(err)
	}
	last := lines[len(lines)-1]
	if len(lines) != maxLogLines || lines[0].Message != "line 6" || last.Level != "info" || len(last.Message) != maxLogMessage {
		t.Fatalf("kept %d lines from %q to a %s line of %d bytes", len(lines), lines[0].Message, last.Level, len(last.Message))
	}
	newer, err := q.Logs(ctx, j.ID, lines[len(lines)-2].ID, 10)
	if err != nil || len(newer) != 1 || newer[0].ID != last.ID {
		t.Fatalf("lines after the second to last = %+v, %v", newer, err)
	}
}

func TestWorkerLogsEachAttempt(t *testing.T) {
	q := newQueue(t)
	startWorker(t, &Worker{Queue: q, Handlers: map[string]Handler{
		"test": func(_ context.Context, r *Run) error {
			r.Log("warn", "row 7 skipped")
			return nil
		},
	}})
	j, err := q.Enqueue(context.Background(), Spec{Kind: "test"})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, q, j.ID, StatusSucceeded)
	lines, err := q.Logs(context.Background(), j.ID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, l := range lines {
		got = append(got, l.Level+" "+l.Message)
	}
	want := []string{fmt.Sprintf("info Attempt 1 of %d started", j.MaxAttempts), "warn row 7 skipped", "info Succeeded"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("log = %q, want %q", got, want)
	}
}
