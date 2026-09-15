package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"jukem/internal/store"
)

// SessionInfo tells the UI whether it is signed in and what it needs.
type SessionInfo struct {
	SetupRequired bool   `json:"setup_required" doc:"True until a password exists"`
	Authenticated bool   `json:"authenticated"`
	Kind          string `json:"kind,omitempty" enum:"session,api_key"`
	CSRFToken     string `json:"csrf_token,omitempty" doc:"Send as X-CSRF-Token on state-changing requests"`
	// SetupComplete is present for a web session; false opens the wizard.
	SetupComplete *bool `json:"setup_complete,omitempty" doc:"False until the setup wizard has finished"`
}

type sessionOutput struct {
	SetCookie *http.Cookie `header:"Set-Cookie"`
	Body      SessionInfo
}

// cookieOutput is a 204 answer that only changes the cookie.
type cookieOutput struct {
	SetCookie *http.Cookie `header:"Set-Cookie"`
}

type passwordInput struct {
	Body struct {
		Password string `json:"password" minLength:"8" maxLength:"256"`
	}
}

type changePasswordInput struct {
	Body struct {
		Current  string `json:"current" doc:"The current password"`
		Password string `json:"password" minLength:"8" maxLength:"256" doc:"The new password"`
	}
}

type apiKeyListOutput struct {
	Body struct {
		Keys []store.APIKey `json:"keys"`
	}
}

type createAPIKeyInput struct {
	Body struct {
		Name      string     `json:"name" minLength:"1" maxLength:"100" doc:"Where the key is used, for example Counter tablet app"`
		ExpiresAt *time.Time `json:"expires_at,omitempty" doc:"Optional expiry"`
	}
}

type createAPIKeyOutput struct {
	Body struct {
		store.APIKey
		Key string `json:"key" doc:"The key itself, shown once"`
	}
}

func (s *Server) registerAuth(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "get-session", Method: http.MethodGet, Path: "/auth/session", Tags: []string{"auth"},
		Summary: "Read the sign-in state", Security: []map[string][]string{},
	}, func(ctx context.Context, _ *struct{}) (*sessionOutput, error) {
		info, err := s.sessionInfo(ctx)
		return &sessionOutput{Body: info}, err
	})

	huma.Register(api, huma.Operation{
		OperationID: "setup-password", Method: http.MethodPost, Path: "/auth/setup", Tags: []string{"auth"},
		Summary: "Set the first password", Description: "Allowed only while no password exists. Starts a web session.",
		DefaultStatus: http.StatusCreated, Security: []map[string][]string{},
	}, func(ctx context.Context, in *passwordInput) (*sessionOutput, error) {
		hash, err := HashPassword(in.Body.Password)
		if err != nil {
			return nil, err
		}
		set, err := s.store.SetPasswordIfNone(ctx, hash)
		if err != nil {
			return nil, err
		}
		if !set {
			return nil, huma.Error409Conflict("a password is already set")
		}
		return s.signIn(ctx)
	})

	huma.Register(api, huma.Operation{
		OperationID: "login", Method: http.MethodPost, Path: "/auth/login", Tags: []string{"auth"},
		Summary: "Start a web session", Security: []map[string][]string{},
	}, func(ctx context.Context, in *passwordInput) (*sessionOutput, error) {
		r := requestFrom(ctx)
		ip := clientIP(r)
		if !s.auth.limiter.allowed(ip) {
			return nil, huma.Error429TooManyRequests("too many failed logins; wait a few minutes")
		}
		hash, exists, err := s.store.PasswordHash(ctx)
		if err != nil {
			return nil, err
		}
		if !exists || !VerifyPassword(hash, in.Body.Password) {
			s.auth.limiter.fail(ip)
			return nil, huma.Error401Unauthorized("wrong password")
		}
		s.auth.limiter.reset(ip)
		return s.signIn(ctx)
	})

	huma.Register(api, huma.Operation{
		OperationID: "logout", Method: http.MethodPost, Path: "/auth/logout", Tags: []string{"auth"},
		Summary: "End the web session", DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, _ *struct{}) (*cookieOutput, error) {
		p, err := webSession(ctx)
		if err != nil {
			return nil, err
		}
		if err := s.store.DeleteSession(ctx, p.Session.ID); err != nil {
			return nil, err
		}
		return &cookieOutput{SetCookie: s.auth.clearCookie()}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "change-password", Method: http.MethodPut, Path: "/auth/password", Tags: []string{"auth"},
		Summary: "Change the password", Description: "Signs out every session, including this one. API keys stay valid; revoke them separately.",
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *changePasswordInput) (*cookieOutput, error) {
		if _, err := webSession(ctx); err != nil {
			return nil, err
		}
		hash, _, err := s.store.PasswordHash(ctx)
		if err != nil {
			return nil, err
		}
		if !VerifyPassword(hash, in.Body.Current) {
			return nil, huma.Error403Forbidden("the current password is wrong")
		}
		newHash, err := HashPassword(in.Body.Password)
		if err != nil {
			return nil, err
		}
		if err := s.store.SetPasswordHash(ctx, newHash); err != nil {
			return nil, err
		}
		return &cookieOutput{SetCookie: s.auth.clearCookie()}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-api-keys", Method: http.MethodGet, Path: "/api-keys", Tags: []string{"auth"},
		Summary: "List API keys",
	}, func(ctx context.Context, _ *struct{}) (*apiKeyListOutput, error) {
		if _, err := webSession(ctx); err != nil {
			return nil, err
		}
		keys, err := s.store.ListAPIKeys(ctx)
		if err != nil {
			return nil, err
		}
		out := &apiKeyListOutput{}
		out.Body.Keys = keys
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "create-api-key", Method: http.MethodPost, Path: "/api-keys", Tags: []string{"auth"},
		Summary: "Create an API key", Description: "The key is returned once and never shown again.",
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *createAPIKeyInput) (*createAPIKeyOutput, error) {
		if _, err := webSession(ctx); err != nil {
			return nil, err
		}
		key := randomToken(32)
		id, err := s.store.CreateAPIKey(ctx, in.Body.Name, hashKey(key), in.Body.ExpiresAt)
		if err != nil {
			return nil, err
		}
		out := &createAPIKeyOutput{}
		out.Body.APIKey = store.APIKey{ID: id, Name: in.Body.Name, CreatedAt: time.Now().UTC(), ExpiresAt: in.Body.ExpiresAt}
		out.Body.Key = key
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-api-key", Method: http.MethodDelete, Path: "/api-keys/{id}", Tags: []string{"auth"},
		Summary: "Revoke an API key", DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *struct {
		ID int64 `path:"id"`
	}) (*struct{}, error) {
		if _, err := webSession(ctx); err != nil {
			return nil, err
		}
		ok, err := s.store.DeleteAPIKey(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, huma.Error404NotFound("no such key")
		}
		return nil, nil
	})
}

// sessionInfo describes the caller of the current request.
func (s *Server) sessionInfo(ctx context.Context) (SessionInfo, error) {
	_, exists, err := s.store.PasswordHash(ctx)
	if err != nil {
		return SessionInfo{}, err
	}
	info := SessionInfo{SetupRequired: !exists}
	if p, ok := PrincipalFrom(ctx); ok {
		info.Authenticated = true
		info.Kind = string(p.Kind)
		info.CSRFToken = p.Session.CSRFToken
		if p.Kind == KindSession {
			info.SetupComplete = s.setupComplete()
		}
	}
	return info, nil
}

// setupComplete reports whether the wizard finished. Without settings
// (maintenance mode) the answer is nil, and the UI does not open it.
func (s *Server) setupComplete() *bool {
	if s.opts.Settings == nil {
		return nil
	}
	done := s.opts.Settings().SetupComplete
	return &done
}

func (s *Server) signIn(ctx context.Context) (*sessionOutput, error) {
	r := requestFrom(ctx)
	se, cookie, err := s.auth.startSession(ctx, r)
	if err != nil {
		return nil, err
	}
	return &sessionOutput{
		SetCookie: cookie,
		Body:      SessionInfo{Authenticated: true, Kind: string(KindSession), CSRFToken: se.CSRFToken, SetupComplete: s.setupComplete()},
	}, nil
}

// webSession returns the principal when it is a browser session. A leaked
// API key cannot manage access.
func webSession(ctx context.Context) (Principal, error) {
	p, ok := PrincipalFrom(ctx)
	if !ok || p.Kind != KindSession {
		return p, huma.Error403Forbidden("this endpoint needs a web session")
	}
	return p, nil
}
