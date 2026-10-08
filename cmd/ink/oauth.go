package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// tokenResponse is a token endpoint's reply.
type tokenResponse struct {
	Access    string `json:"access_token"`
	Refresh   string `json:"refresh_token"`
	ExpiresIn int    `json:"expires_in"`
	Scope     string `json:"scope"`
}

// creds returns the reply as creds, keeping prevRefresh if the reply has no
// new refresh token.
func (t tokenResponse) creds(prevRefresh string) creds {
	return creds{Access: t.Access, Refresh: cmp.Or(t.Refresh, prevRefresh), Expires: time.Now().Add(time.Duration(t.ExpiresIn) * time.Second)}
}

// oauthError is an error an OAuth endpoint returned.
type oauthError struct {
	Status      int
	Code        string `json:"error"`
	Description string `json:"error_description"`
}

func (e *oauthError) Error() string { return cmp.Or(e.Description, e.Code, http.StatusText(e.Status)) }

// postForm posts form to endpoint and decodes the JSON reply into out,
// unless out is nil.
func postForm(ctx context.Context, endpoint string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		e := &oauthError{Status: resp.StatusCode}
		_ = json.Unmarshal(raw, e)
		return e
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// concurrentWait is how long accessToken waits for another ink's refresh to
// land, and how often it looks.
var concurrentWait, concurrentPoll = 2 * time.Second, 200 * time.Millisecond

// accessToken returns the profile's access token, refreshing it within a
// minute of its expiry.
func accessToken(ctx context.Context, name string, p profile) (string, error) {
	c, err := loadCreds(name)
	if errors.Is(err, errNoCreds) {
		return "", fmt.Errorf("profile %s has no saved login; log in with: ink login --profile %s %s", name, name, p.URL)
	}
	if err != nil {
		return "", err
	}
	if time.Until(c.Expires) > time.Minute {
		return c.Access, nil
	}
	var toks tokenResponse
	err = postForm(ctx, p.URL+"/oauth/token", url.Values{"grant_type": {"refresh_token"},
		"refresh_token": {c.Refresh}, "client_id": {p.ClientID}}, &toks)
	if oe := (*oauthError)(nil); errors.As(err, &oe) && oe.Code == "invalid_grant" {
		// Another ink may have just refreshed but not saved yet, so look a few times.
		for waited := time.Duration(0); ; waited += concurrentPoll {
			if again, err := loadCreds(name); err == nil && again.Refresh != c.Refresh && time.Until(again.Expires) > time.Minute {
				return again.Access, nil
			}
			if waited >= concurrentWait {
				break
			}
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(concurrentPoll):
			}
		}
		return "", fmt.Errorf("the login for profile %s has ended; log in again with: ink login --profile %s %s", name, name, p.URL)
	}
	if err != nil {
		return "", fmt.Errorf("refresh the login for profile %s: %w", name, err)
	}
	if _, err := saveCreds(name, toks.creds(c.Refresh)); err != nil {
		// The server rotated the refresh token, so the saved one is now dead.
		return "", fmt.Errorf("refreshed the login for profile %s but couldn't save it (%w); log in again with: ink login --profile %s %s", name, err, name, p.URL)
	}
	return toks.Access, nil
}
