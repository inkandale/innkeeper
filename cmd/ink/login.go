package main

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// loginWait is how long login waits for the browser to come back.
var loginWait = 5 * time.Minute

// openBrowser opens page in the default browser. Tests replace it.
var openBrowser = func(page string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", page).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", page).Start()
	default:
		return exec.Command("xdg-open", page).Start()
	}
}

func loginCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "login <url>",
		Short: "Log in to a server through your browser",
		Long: `Login opens the server's login page in your browser, where you choose ink's tier.
It saves the login as the profile --profile names, or "default". The first profile
becomes the default. The server needs INNKEEPER_PUBLIC_URL set.`,
		Example: "  ink login https://innkeeper.example.com\n  ink login --profile work https://inn.work.example",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, _ := cmd.Flags().GetString("profile")
			name = cmp.Or(name, "default")
			if err := checkProfileName(name); err != nil {
				return err
			}
			c, err := loadConfig()
			if err != nil {
				return err
			}
			p, toks, err := login(cmd.Context(), cmd.ErrOrStderr(), strings.TrimSuffix(args[0], "/"))
			if err != nil {
				return err
			}
			// Read the login this one replaces before saving over it.
			old, hadOld := c.Profiles[name]
			oldCreds, oldErr := loadCreds(name)
			where, err := saveCreds(name, toks.creds(""))
			if err != nil {
				return err
			}
			c.Profiles[name] = p
			c.Default = cmp.Or(c.Default, name)
			if err := c.save(); err != nil {
				return err
			}
			if hadOld && oldErr == nil {
				// Revoke only once the new login is saved, so a failed save leaves the old one.
				_ = postForm(cmd.Context(), old.URL+"/oauth/revoke",
					url.Values{"token": {oldCreds.Refresh}, "client_id": {old.ClientID}}, nil)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Logged in to %s with the %s tier as profile %s. The login is in %s.\n",
				p.URL, toks.Scope, name, where)
			return err
		},
	}
}

// login runs the OAuth flow against server and returns the profile and the
// tokens it got.
func login(ctx context.Context, stderr io.Writer, server string) (profile, tokenResponse, error) {
	if !isWebURL(server) {
		return profile{}, tokenResponse{}, fmt.Errorf("%s isn't an http or https address", server)
	}
	var meta struct {
		Issuer     string `json:"issuer"`
		Revocation string `json:"revocation_endpoint"`
	}
	if err := getJSON(ctx, server+"/.well-known/oauth-authorization-server", &meta); err != nil || meta.Issuer == "" {
		return profile{}, tokenResponse{}, fmt.Errorf("%s doesn't offer login (%v); set INNKEEPER_PUBLIC_URL on the server, or use INNKEEPER_URL and INNKEEPER_TOKEN", server, err)
	}
	if meta.Revocation == "" {
		return profile{}, tokenResponse{}, fmt.Errorf("%s is too old for ink login; upgrade it", server)
	}
	// A server naming another issuer could hand the OS browser launcher any scheme.
	if meta.Issuer != server {
		if isWebURL(meta.Issuer) {
			return profile{}, tokenResponse{}, fmt.Errorf("%s says its address is %s; log in with: ink login %s", server, meta.Issuer, meta.Issuer)
		}
		return profile{}, tokenResponse{}, fmt.Errorf("%s named an invalid address for itself", server)
	}
	issuer := server
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return profile{}, tokenResponse{}, err
	}
	defer ln.Close()
	redirect := fmt.Sprintf("http://%s/callback", ln.Addr())
	clientID, err := register(ctx, issuer)
	if err != nil {
		return profile{}, tokenResponse{}, err
	}
	verifier, state := randomString(32), randomString(16)
	sum := sha256.Sum256([]byte(verifier))
	page := issuer + "/oauth/authorize?" + url.Values{"response_type": {"code"}, "client_id": {clientID},
		"redirect_uri": {redirect}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"}, "state": {state}, "resource": {issuer + "/api"}}.Encode()
	fmt.Fprintf(stderr, "Opening %s\nIf no browser opens, visit that address.\n", page)
	_ = openBrowser(page)
	code, err := awaitCode(ctx, ln, state, issuer)
	if err != nil {
		return profile{}, tokenResponse{}, err
	}
	var toks tokenResponse
	err = postForm(ctx, issuer+"/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code},
		"client_id": {clientID}, "redirect_uri": {redirect}, "code_verifier": {verifier}}, &toks)
	if err == nil && toks.Access == "" {
		err = fmt.Errorf("%s answered the login without an access token", issuer)
	}
	return profile{URL: issuer, ClientID: clientID}, toks, err
}

// isWebURL reports whether s is an absolute http or https URL with a host.
func isWebURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func randomString(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func getJSON(ctx context.Context, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	// No redirects: metadata from another origin isn't the server's.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New(resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

// register registers ink as an OAuth client of issuer and returns its ID.
// The redirect has no port, so the server accepts whichever one ink gets.
func register(ctx context.Context, issuer string) (string, error) {
	host, _ := os.Hostname()
	body, _ := json.Marshal(map[string]any{"client_name": "ink on " + cmp.Or(host, "this computer"),
		"redirect_uris": []string{"http://127.0.0.1/callback"}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, issuer+"/oauth/register", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		ClientID    string `json:"client_id"`
		Description string `json:"error_description"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out)
	if resp.StatusCode != http.StatusCreated || out.ClientID == "" {
		return "", fmt.Errorf("register with %s: %s", issuer, cmp.Or(out.Description, resp.Status))
	}
	return out.ClientID, nil
}

// awaitCode serves the redirect on ln and returns the code the browser
// brings back.
func awaitCode(ctx context.Context, ln net.Listener, state, issuer string) (string, error) {
	type result struct {
		code string
		err  error
	}
	done := make(chan result, 1)
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/callback" {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		if q.Get("state") != state {
			// Anything local can hit this port; only the real redirect ends the wait.
			http.Error(w, "wrong state", http.StatusBadRequest)
			return
		}
		res := result{code: q.Get("code")}
		switch {
		case q.Get("iss") != issuer:
			res.err = fmt.Errorf("the login came back from %q, not %s", q.Get("iss"), issuer)
		case q.Get("error") != "":
			res.err = fmt.Errorf("login refused: %q", cmp.Or(q.Get("error_description"), q.Get("error")))
		case res.code == "":
			res.err = errors.New("the login came back without a code")
		}
		if res.err != nil {
			http.Error(w, res.err.Error(), http.StatusBadRequest)
		} else {
			fmt.Fprintln(w, "ink is logged in. You can close this tab.")
		}
		select {
		case done <- res:
		default:
		}
	})}
	go srv.Serve(ln)
	defer func() {
		// Shutdown, not Close, so the page reaches the browser before the connection drops.
		sctx, stop := context.WithTimeout(context.Background(), 2*time.Second)
		defer stop()
		_ = srv.Shutdown(sctx)
	}()
	ctx, cancel := context.WithTimeout(ctx, loginWait)
	defer cancel()
	select {
	case r := <-done:
		return r.code, r.err
	case <-ctx.Done():
		return "", errors.New("timed out waiting for the login in the browser")
	}
}
