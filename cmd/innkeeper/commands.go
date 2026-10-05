package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"
)

// jsonArg returns args[i] if it is valid JSON, or def if args has no index i.
func jsonArg(args []string, i int, def string) (json.RawMessage, error) {
	if len(args) <= i {
		return json.RawMessage(def), nil
	}
	if !json.Valid([]byte(args[i])) {
		return nil, fmt.Errorf("%q is not valid JSON", args[i])
	}
	return json.RawMessage(args[i]), nil
}

// call runs `innkeeper call METHOD PATH [JSON]` and prints the response body.
func call(args []string) error {
	if len(args) < 2 || len(args) > 3 {
		return errors.New(usage)
	}
	c, err := newClient()
	if err != nil {
		return err
	}
	var body []byte
	if len(args) == 3 {
		if body, err = jsonArg(args, 2, ""); err != nil {
			return err
		}
	}
	out, err := c.do(context.Background(), strings.ToUpper(args[0]), args[1], body)
	if err != nil {
		return err
	}
	fmt.Println(strings.TrimSuffix(string(out), "\n"))
	return nil
}

// startJob posts body to path, which starts a job, and says how to follow it.
func startJob(path string, body any) error {
	c, err := newClient()
	if err != nil {
		return err
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	out, err := c.do(context.Background(), "POST", path, raw)
	if err != nil {
		return err
	}
	var j struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(out, &j); err != nil {
		return err
	}
	fmt.Printf("started job %d; follow it with: innkeeper jobs watch %d\n", j.ID, j.ID)
	return nil
}

// importCmd runs `innkeeper import INSTANCE_ID [PARAMS_JSON]`.
func importCmd(args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return errors.New(usage)
	}
	id, err := parseID(args[0])
	if err != nil {
		return err
	}
	params, err := jsonArg(args, 1, `{}`)
	if err != nil {
		return err
	}
	return startJob("/imports", map[string]any{"instance_id": json.Number(id), "params": params})
}

// bulkCmd runs `innkeeper bulk archive|unarchive QUERY_JSON`.
func bulkCmd(args []string) error {
	if len(args) != 2 || args[0] != "archive" && args[0] != "unarchive" {
		return errors.New(usage)
	}
	query, err := jsonArg(args, 1, "")
	if err != nil {
		return err
	}
	return startJob("/bulk", map[string]any{"query": query, "action": args[0]})
}

// pushCmd runs `innkeeper push INSTANCE_ID CARD_ID|QUERY_JSON [--new-copy]`.
func pushCmd(args []string) error {
	newCopy := len(args) == 3 && args[2] == "--new-copy"
	if len(args) != 2 && !newCopy {
		return errors.New(usage)
	}
	inst, err := parseID(args[0])
	if err != nil {
		return err
	}
	if strings.HasPrefix(strings.TrimSpace(args[1]), "{") {
		query, err := jsonArg(args, 1, "")
		if err != nil {
			return err
		}
		return startJob("/pushes", map[string]any{"instance_id": json.Number(inst), "query": query, "new_copy": newCopy})
	}
	card, err := parseID(args[1])
	if err != nil {
		return err
	}
	return startJob("/cards/"+card+"/push", map[string]any{"instance_id": json.Number(inst), "new_copy": newCopy})
}

// export runs `innkeeper export <card-id> [file [format]]`. A .png file
// defaults to png, anything else to json.
func export(args []string) error {
	if len(args) < 1 || len(args) > 3 {
		return errors.New(usage)
	}
	id, err := parseID(args[0])
	if err != nil {
		return err
	}
	format := "json"
	switch {
	case len(args) == 3:
		format = args[2]
	case len(args) == 2 && strings.EqualFold(filepath.Ext(args[1]), ".png"):
		format = "png"
	}
	c, err := newClient()
	if err != nil {
		return err
	}
	out, err := c.do(context.Background(), "GET", "/cards/"+id+"/export?format="+url.QueryEscape(format), nil)
	if err != nil {
		return err
	}
	if len(args) == 1 {
		_, err = os.Stdout.Write(out)
		return err
	}
	return os.WriteFile(args[1], out, 0o644)
}

// jobsCmd runs `innkeeper jobs [show|cancel|watch ID]`.
func jobsCmd(args []string) error {
	var id string
	if len(args) != 0 {
		if len(args) != 2 || args[0] != "show" && args[0] != "cancel" && args[0] != "watch" {
			return errors.New(usage)
		}
		var err error
		if id, err = parseID(args[1]); err != nil {
			return err
		}
	}
	c, err := newClient()
	if err != nil {
		return err
	}
	ctx := context.Background()
	if len(args) == 0 {
		out, err := c.do(ctx, "GET", "/jobs", nil)
		if err != nil {
			return err
		}
		var list []struct {
			ID        int64     `json:"id"`
			Kind      string    `json:"kind"`
			Status    string    `json:"status"`
			CreatedAt time.Time `json:"created_at"`
			Error     *string   `json:"error"`
		}
		if err := json.Unmarshal(out, &list); err != nil {
			return err
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tKIND\tSTATUS\tCREATED\tERROR")
		for _, j := range list {
			e := ""
			if j.Error != nil {
				e = *j.Error
			}
			fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n", j.ID, j.Kind, j.Status, j.CreatedAt.Local().Format(time.DateTime), e)
		}
		return tw.Flush()
	}
	switch args[0] {
	case "show", "cancel":
		method, path := "GET", "/jobs/"+id
		if args[0] == "cancel" {
			method, path = "POST", path+"/cancel"
		}
		out, err := c.do(ctx, method, path, nil)
		if err != nil {
			return err
		}
		fmt.Println(strings.TrimSuffix(string(out), "\n"))
		return nil
	case "watch":
		return watchJob(ctx, c, id)
	}
	return errors.New(usage)
}

// watchJob prints a line each time the job changes, and fails unless the job
// succeeds.
func watchJob(ctx context.Context, c *client, id string) error {
	status := ""
	err := c.stream(ctx, "/jobs/"+id+"/events", func(data []byte) error {
		var j struct {
			Status   string          `json:"status"`
			Progress json.RawMessage `json:"progress"`
			Children map[string]int  `json:"children"`
			Outcomes map[string]int  `json:"outcomes"`
			Error    *string         `json:"error"`
		}
		if err := json.Unmarshal(data, &j); err != nil {
			return err
		}
		status = j.Status
		line := j.Status + " " + string(j.Progress)
		if len(j.Children) > 0 {
			line += fmt.Sprintf(" children %v", j.Children)
		}
		if len(j.Outcomes) > 0 {
			line += fmt.Sprintf(" outcomes %v", j.Outcomes)
		}
		if j.Error != nil {
			line += " error: " + *j.Error
		}
		fmt.Println(line)
		return nil
	})
	if err != nil {
		return err
	}
	switch status {
	case "succeeded":
		return nil
	case "failed":
		return errors.New("job failed")
	case "":
		status = "unknown"
	}
	return fmt.Errorf("job stream ended while the job was %s; check it with: innkeeper jobs show %s", status, id)
}
