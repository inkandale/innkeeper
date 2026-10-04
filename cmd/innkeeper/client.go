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
)

// client calls the HTTP API at INNKEEPER_URL with INNKEEPER_TOKEN.
type client struct{ base, token string }

func newClient() (*client, error) {
	c := &client{base: strings.TrimSuffix(os.Getenv("INNKEEPER_URL"), "/"), token: os.Getenv("INNKEEPER_TOKEN")}
	if c.base == "" || c.token == "" {
		return nil, errors.New("set INNKEEPER_URL and INNKEEPER_TOKEN; make a token with: innkeeper token create <name>")
	}
	return c, nil
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
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 300 {
		return resp, nil
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var p struct {
		Detail string `json:"detail"`
		Errors []struct {
			Message  string `json:"message"`
			Location string `json:"location"`
		} `json:"errors"`
	}
	if json.Unmarshal(raw, &p) != nil || p.Detail == "" {
		return nil, fmt.Errorf("%s %s: %s", method, path, resp.Status)
	}
	msg := p.Detail
	for _, e := range p.Errors {
		msg += fmt.Sprintf("; %s: %s", e.Location, e.Message)
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
	if _, err := strconv.ParseInt(s, 10, 64); err != nil {
		return "", fmt.Errorf("%q is not an ID", s)
	}
	return s, nil
}
