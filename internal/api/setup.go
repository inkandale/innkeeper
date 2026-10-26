package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/danielgtaylor/huma/v2"

	"github.com/inkandale/innkeeper/internal/auth"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/protocol"
)

// bcryptHash matches the shape of a bcrypt hash, as password-login makes.
var bcryptHash = regexp.MustCompile(`^\$2[aby]\$(0[4-9]|[12][0-9]|3[01])\$[./A-Za-z0-9]{53}$`)

// claimed reports whether the server has a usable login provider.
func (s *Server) claimed(ctx context.Context) (bool, error) {
	list, err := s.loginProviders(ctx)
	return len(list) > 0, err
}

// Bootstrap runs at startup. On an unclaimed server it creates the owner's
// password login from ownerHash, or without one, logs a setup code.
func (s *Server) Bootstrap(ctx context.Context, ownerHash string) error {
	claimed, err := s.claimed(ctx)
	if err != nil {
		return err
	}
	if claimed {
		if ownerHash != "" {
			s.Log.Warn("INNKEEPER_OWNER_PASSWORD_HASH is ignored: the server already has a login provider")
		}
		return nil
	}
	if ownerHash == "" {
		s.ensureSetupCode()
		return nil
	}
	if !bcryptHash.MatchString(ownerHash) {
		return errors.New("INNKEEPER_OWNER_PASSWORD_HASH is not a bcrypt hash; make one with: password-login hash")
	}
	if _, err := s.createOwnerLogin(ctx, "password-login", map[string]string{"password_hash": ownerHash}, "owner"); err != nil {
		return fmt.Errorf("create the owner's login from INNKEEPER_OWNER_PASSWORD_HASH: %w", err)
	}
	s.Log.Info("created the owner's login from INNKEEPER_OWNER_PASSWORD_HASH")
	return nil
}

// createOwnerLogin creates the owner's login instance. An old, unusable
// login provider may still hold the name "Login", so it tries "Login 2" and on.
func (s *Server) createOwnerLogin(ctx context.Context, plugin string, secrets map[string]string, subject string) (int64, error) {
	name := "Login"
	for n := 2; ; n++ {
		id, err := s.Registry.CreateInstance(ctx, plugins.NewInstance{Plugin: plugin, Name: &name,
			Secrets: secrets, OwnerSubject: &subject})
		if !errors.Is(err, plugins.ErrNameTaken) || n > 10 {
			return id, err
		}
		name = fmt.Sprintf("Login %d", n)
	}
}

// setupLogEvery limits how often opening /setup logs the code, since
// anyone can open it.
const setupLogEvery = time.Minute

// ensureSetupCode returns the setup code, making one if needed. It logs the
// code unless it did within setupLogEvery, so the owner finds it near the end
// of the log.
func (s *Server) ensureSetupCode() string {
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	if s.setupCode == "" {
		s.setupCode = groupCode(rand.Text())
		s.setupLogged = time.Time{}
	}
	if time.Since(s.setupLogged) >= setupLogEvery {
		s.setupLogged = time.Now()
		s.Log.Warn("server is unclaimed; open /setup and enter the setup code", "code", s.setupCode)
	}
	return s.setupCode
}

func (s *Server) currentSetupCode() string {
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	return s.setupCode
}

func (s *Server) clearSetupCode() {
	s.setupMu.Lock()
	s.setupCode = ""
	s.setupMu.Unlock()
}

// groupCode splits code into groups of four for typing.
func groupCode(code string) string {
	var groups []string
	for len(code) > 4 {
		groups = append(groups, code[:4])
		code = code[4:]
	}
	return strings.Join(append(groups, code), "-")
}

// normalizeCode drops dashes and spaces and upper-cases the rest, so a
// typed code matches however it was grouped.
func normalizeCode(code string) string {
	return strings.Map(func(r rune) rune {
		if r == '-' || unicode.IsSpace(r) {
			return -1
		}
		return unicode.ToUpper(r)
	}, code)
}

// codeMatches compares a typed code with the setup code in constant time.
func codeMatches(want, got string) bool {
	return want != "" && subtle.ConstantTimeCompare([]byte(normalizeCode(want)), []byte(normalizeCode(got))) == 1
}

type setupPlugin struct {
	Plugin string          `json:"plugin"`
	Form   json.RawMessage `json:"form" doc:"JSON Schema of the fields to ask for."`
}

type setupOut struct{ Body []setupPlugin }

type claimIn struct {
	Body struct {
		Code   string            `json:"code" minLength:"1" doc:"The setup code from the server log."`
		Plugin string            `json:"plugin" minLength:"1"`
		Params map[string]string `json:"params"`
	}
}

// setupInstance runs an auth plugin before it has an instance.
func setupInstance(m *protocol.Manifest) pluginhost.Instance {
	return pluginhost.Instance{ID: "setup-" + m.Name, Version: "0", Manifest: m,
		Params: protocol.InitializeParams{Config: map[string]any{}, Secrets: map[string]string{}}}
}

func (s *Server) setupRoutes(api huma.API) {
	tags := []string{"Auth"}
	huma.Register(api, huma.Operation{OperationID: "get-setup", Method: http.MethodGet, Path: "/setup",
		Summary:     "List the ways to claim this server",
		Description: "Lists the login plugins that can claim a server with no login provider. 404 once the server is claimed.",
		Tags:        tags, Metadata: public, Security: noAuth},
		func(ctx context.Context, _ *struct{}) (*setupOut, error) {
			claimed, err := s.claimed(ctx)
			if err != nil {
				return nil, s.fail(err)
			}
			if claimed {
				return nil, huma.Error404NotFound("the server is already set up")
			}
			s.ensureSetupCode()
			out := &setupOut{Body: []setupPlugin{}}
			manifests := s.Registry.Manifests()
			names := slices.Sorted(maps.Keys(manifests))
			for _, name := range names {
				m := manifests[name]
				if !m.Has(protocol.CapabilityAuth) {
					continue
				}
				var res protocol.AuthSetupResult
				err := s.Plugins.Call(ctx, setupInstance(m), protocol.MethodAuthSetup, nil, &res)
				var rpcErr *protocol.Error
				if errors.As(err, &rpcErr) && rpcErr.Code == protocol.CodeMethodNotFound {
					continue
				}
				if err != nil {
					s.Log.Warn("auth.setup failed", "plugin", name, "err", err)
					continue
				}
				out.Body = append(out.Body, setupPlugin{Plugin: name, Form: res.Form})
			}
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "claim-server", Method: http.MethodPost, Path: "/setup",
		Summary:     "Claim this server",
		Description: "Creates the owner's login provider with the setup code from the server log, and sets the session cookie.",
		Tags:        tags, Metadata: public, Security: noAuth, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *claimIn) (*cookiesOut, error) {
			select {
			case s.loginSlot <- struct{}{}:
				defer func() { <-s.loginSlot }()
			case <-ctx.Done():
				return nil, huma.Error503ServiceUnavailable("login busy, try again")
			}
			// One answer for a wrong code and a claimed server, so the
			// page reveals neither.
			refuse := func() error {
				time.Sleep(failedLoginDelay)
				return huma.Error403Forbidden("setup failed; check the setup code in the server log")
			}
			if !codeMatches(s.currentSetupCode(), in.Body.Code) {
				return nil, refuse()
			}
			m := s.Registry.Manifests()[in.Body.Plugin]
			if m == nil || !m.Has(protocol.CapabilityAuth) {
				return nil, huma.Error400BadRequest("no such login plugin: " + in.Body.Plugin)
			}
			var res protocol.AuthSetupCompleteResult
			err := s.Plugins.Call(ctx, setupInstance(m), protocol.MethodAuthSetupComplete,
				protocol.AuthSetupCompleteParams{Params: in.Body.Params}, &res)
			var rpcErr *protocol.Error
			if errors.As(err, &rpcErr) {
				return nil, huma.Error400BadRequest(rpcErr.Message)
			}
			if err != nil {
				return nil, s.fail(err)
			}
			if res.Subject == "" {
				return nil, huma.Error502BadGateway("plugin: auth.setup.complete returned no subject")
			}
			// Checked last, just before the claim; loginSlot keeps logins out meanwhile.
			if claimed, err := s.claimed(ctx); err != nil {
				return nil, s.fail(err)
			} else if claimed {
				return nil, refuse()
			}
			id, err := s.createOwnerLogin(ctx, m.Name, res.Secrets, res.Subject)
			if err != nil {
				return nil, s.fail(err)
			}
			s.clearSetupCode()
			s.Log.Info("server claimed", "plugin", m.Name, "instance", id)
			secret, err := s.Auth.CreateSession(ctx, id, res.Subject)
			if err != nil {
				return nil, s.fail(err)
			}
			return &cookiesOut{SetCookie: []*http.Cookie{s.cookie(ctx, sessionCookie, secret, auth.SessionTTL)}}, nil
		})
}
