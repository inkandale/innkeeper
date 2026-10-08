package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/davidgibbons/innkeeper/internal/plugins"
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
	owner := "owner"
	if _, err := s.Registry.CreateInstance(ctx, plugins.NewInstance{Plugin: "password-login", Name: "Login",
		Secrets: map[string]string{"password_hash": ownerHash}, OwnerSubject: &owner}); err != nil {
		return fmt.Errorf("create the owner's login from INNKEEPER_OWNER_PASSWORD_HASH: %w", err)
	}
	s.Log.Info("created the owner's login from INNKEEPER_OWNER_PASSWORD_HASH")
	return nil
}

// ensureSetupCode returns the setup code, making and logging one if needed.
func (s *Server) ensureSetupCode() string {
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	if s.setupCode == "" {
		s.setupCode = groupCode(rand.Text())
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
