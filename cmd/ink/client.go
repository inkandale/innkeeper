package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// client calls an Innkeeper server's HTTP API with a bearer token.
type client struct{ base, token string }

// newClient uses INNKEEPER_URL and INNKEEPER_TOKEN if the token is set, and
// otherwise the profile --profile names, or the default one.
func newClient(cmd *cobra.Command) (*client, error) {
	if tok := os.Getenv("INNKEEPER_TOKEN"); tok != "" {
		root := strings.TrimSuffix(os.Getenv("INNKEEPER_URL"), "/")
		if root == "" {
			return nil, errors.New("INNKEEPER_TOKEN needs INNKEEPER_URL, the server's address")
		}
		return &client{base: root + "/api", token: tok}, nil
	}
	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}
	name := cfg.profileFor(cmd)
	p, ok := cfg.Profiles[name]
	if !ok && name == "" {
		return nil, errors.New("log in with: ink login <url>; or set INNKEEPER_URL and INNKEEPER_TOKEN")
	}
	if !ok {
		return nil, fmt.Errorf("no profile %q; log in with: ink login --profile %s <url>", name, name)
	}
	tok, err := accessToken(cmd.Context(), name, p)
	if err != nil {
		return nil, err
	}
	return &client{base: p.URL + "/api", token: tok}, nil
}

func (c *client) request(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, r)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.send(req, path)
}

// send sends req, a request for path, with the token, and turns an error
// status into an error with the API's explanation.
func (c *client) send(req *http.Request, path string) (*http.Response, error) {
	method := req.Method
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 300 {
		return resp, nil
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var p struct {
		Title  string `json:"title"`
		Detail string `json:"detail"`
		Errors []struct {
			Message  string `json:"message"`
			Location string `json:"location"`
		} `json:"errors"`
	}
	var msg string
	if json.Unmarshal(raw, &p) != nil {
		msg = strings.TrimSpace(string(raw[:min(len(raw), 200)]))
	} else if msg = p.Detail; msg == "" {
		msg = p.Title
	}
	if msg == "" {
		return nil, fmt.Errorf("%s %s: %s", method, path, resp.Status)
	}
	for _, e := range p.Errors {
		if e.Location == "" {
			msg += "; " + e.Message
		} else {
			msg += fmt.Sprintf("; %s: %s", e.Location, e.Message)
		}
	}
	return nil, fmt.Errorf("%s %s: %d %s", method, path, resp.StatusCode, msg)
}

// do sends body as JSON, or nothing if it is nil, and returns the response
// body. An error status becomes an error with the API's explanation.
func (c *client) do(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	resp, err := c.request(ctx, method, path, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// stream calls fn with each server-sent event's data until the stream ends.
func (c *client) stream(ctx context.Context, path string, fn func(data []byte) error) error {
	resp, err := c.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
			if err := fn([]byte(data)); err != nil {
				return err
			}
		}
	}
	return sc.Err()
}

// parseID checks that s is a numeric ID before it goes into a URL path.
func parseID(s string) (string, error) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return "", fmt.Errorf("%q is not an ID", s)
	}
	return strconv.FormatInt(n, 10), nil
}
