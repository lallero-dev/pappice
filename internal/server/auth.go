package server

import (
	"errors"
	"mime"
	"net/http"
	"strings"
	"time"

	"pappice/internal/security"
	"pappice/internal/store"
)

type authContext struct {
	User         store.User
	CSRF         string
	SessionToken string
	ViaToken     bool
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	setupRequired, err := s.store.SetupRequired()
	if err != nil {
		respondStoreError(w, err)
		return
	}
	auth, err := s.currentAuth(r)
	authenticated := err == nil
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		respondStoreError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"authenticated": authenticated,
		"needs_setup":   setupRequired,
		"user":          nullableUser(auth.User, authenticated),
		"csrf_token":    nullableString(auth.CSRF, authenticated && !auth.ViaToken),
	})
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if !s.requireSessionRequest(w, r) {
		return
	}
	var input store.CreateUser
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Event = store.EventContext{Enabled: true, IP: s.clientIP(r)}
	user, err := s.store.CreateFirstAdmin(input)
	if err != nil {
		respondStoreError(w, err)
		return
	}
	csrf, ok := s.createSession(w, user.ID)
	if !ok {
		return
	}
	s.dispatchEventsSoon()
	respondJSON(w, http.StatusCreated, map[string]any{
		"user":       store.ToPublicUser(user),
		"csrf_token": csrf,
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if !s.requireSessionRequest(w, r) {
		return
	}
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	limitKey := "login|" + s.clientIP(r) + "|" + strings.ToLower(strings.TrimSpace(input.Email))
	if !s.loginLimiter.Allow(limitKey, time.Now().UTC()) {
		respondRateLimited(w)
		return
	}
	user, err := s.store.Authenticate(input.Email, input.Password)
	if err != nil {
		if errors.Is(err, store.ErrPasswordResetRequired) {
			respondError(w, http.StatusUnauthorized, "password setup or reset is required; use the emailed link or contact an admin")
			return
		}
		respondError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	csrf, ok := s.createSession(w, user.ID)
	if !ok {
		return
	}
	s.loginLimiter.Reset(limitKey)
	respondJSON(w, http.StatusOK, map[string]any{
		"user":       store.ToPublicUser(user),
		"csrf_token": csrf,
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if !s.requireBrowserSession(w, auth) {
		return
	}
	if err := s.store.DeleteSession(auth.SessionToken); err != nil {
		respondStoreError(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAccountLinkByToken(w http.ResponseWriter, r *http.Request) {
	token := trimRoutePrefix(r.URL.Path, "/api/account-links/")
	if token == "" || strings.Contains(token, "/") {
		http.NotFound(w, r)
		return
	}
	limitKey := "account-link|" + s.clientIP(r) + "|" + security.HashToken(token)
	switch r.Method {
	case http.MethodGet:
		if !s.accountLinkLimiter.Allow(limitKey, time.Now().UTC()) {
			respondRateLimited(w)
			return
		}
		link, user, err := s.store.GetAccountLink(token)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				s.respondAccountLinkError(w, token)
				return
			}
			respondStoreError(w, err)
			return
		}
		respondJSON(w, http.StatusOK, map[string]any{
			"purpose":    link.Purpose,
			"expires_at": link.ExpiresAt,
			"user":       store.ToPublicUser(user),
		})
	case http.MethodPost:
		if !s.requireSessionRequest(w, r) {
			return
		}
		if !s.accountLinkLimiter.Allow(limitKey, time.Now().UTC()) {
			respondRateLimited(w)
			return
		}
		var input struct {
			Password string `json:"password"`
		}
		if !decodeJSON(w, r, &input) {
			return
		}
		user, err := s.store.ConsumeAccountLink(token, input.Password, store.EventContext{Enabled: true, IP: s.clientIP(r)})
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				s.respondAccountLinkError(w, token)
				return
			}
			respondStoreError(w, err)
			return
		}
		csrf, ok := s.createSession(w, user.ID)
		if !ok {
			return
		}
		s.accountLinkLimiter.Reset(limitKey)
		s.dispatchEventsSoon()
		respondJSON(w, http.StatusOK, map[string]any{
			"user":       store.ToPublicUser(user),
			"csrf_token": csrf,
		})
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) respondAccountLinkError(w http.ResponseWriter, token string) {
	status, err := s.store.AccountLinkStatus(token)
	if err != nil {
		respondJSON(w, http.StatusNotFound, map[string]any{
			"error":  "This account link is invalid. Ask an administrator for a new setup or reset link.",
			"reason": "invalid",
		})
		return
	}
	reason := "invalid"
	message := "This account link is no longer valid. Ask an administrator for a new one."
	code := http.StatusGone
	action := "account"
	if status.Purpose == "setup" || status.Purpose == "reset" {
		action = status.Purpose
	}
	switch {
	case status.UserDisabled:
		code = http.StatusForbidden
		reason = "disabled"
		message = "This account is disabled. Contact an administrator."
	case status.UsedAt != nil:
		reason = "used"
		message = "This " + action + " link has already been used. Sign in or ask an administrator for a new link."
	case !status.ExpiresAt.After(time.Now().UTC()):
		reason = "expired"
		message = "This " + action + " link expired on " + status.ExpiresAt.Format("2006-01-02 15:04 MST") + ". Ask an administrator for a new link."
	}
	respondJSON(w, code, map[string]any{
		"error":      message,
		"reason":     reason,
		"purpose":    status.Purpose,
		"expires_at": status.ExpiresAt,
	})
}

func (s *Server) createSession(w http.ResponseWriter, userID int64) (string, bool) {
	token, csrf, expires, err := s.store.CreateSessionFor(userID, s.options.SessionTTL)
	if err != nil {
		respondStoreError(w, err)
		return "", false
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(time.Until(expires).Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
	return csrf, true
}

func (s *Server) currentAuth(r *http.Request) (authContext, error) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil && cookie.Value != "" {
		user, csrf, err := s.store.UserBySession(cookie.Value)
		if err == nil {
			return authContext{User: user, CSRF: csrf, SessionToken: cookie.Value}, nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return authContext{}, err
		}
	}
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(header) > 7 && strings.EqualFold(header[:7], "Bearer ") {
		token := strings.TrimSpace(header[7:])
		user, err := s.store.UserByAPIToken(token)
		if err == nil {
			return authContext{User: user, ViaToken: true}, nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return authContext{}, err
		}
	}
	return authContext{}, store.ErrNotFound
}

func (s *Server) requireAuth(w http.ResponseWriter, r *http.Request) (authContext, bool) {
	setupRequired, err := s.store.SetupRequired()
	if err != nil {
		respondStoreError(w, err)
		return authContext{}, false
	}
	if setupRequired {
		respondError(w, http.StatusConflict, "setup is required")
		return authContext{}, false
	}
	auth, err := s.currentAuth(r)
	if errors.Is(err, store.ErrNotFound) {
		respondError(w, http.StatusUnauthorized, "authentication is required")
		return authContext{}, false
	}
	if err != nil {
		respondStoreError(w, err)
		return authContext{}, false
	}
	if isUnsafeMethod(r.Method) && !auth.ViaToken {
		if !s.verifyCSRF(w, r, auth.CSRF) {
			return authContext{}, false
		}
	}
	return auth, true
}

func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) (authContext, bool) {
	auth, ok := s.requireAuth(w, r)
	if !ok {
		return authContext{}, false
	}
	if !isAdmin(auth.User) {
		respondError(w, http.StatusForbidden, "admin role is required")
		return authContext{}, false
	}
	return auth, true
}

func (s *Server) requireStaff(w http.ResponseWriter, r *http.Request) (authContext, bool) {
	auth, ok := s.requireAuth(w, r)
	if !ok {
		return authContext{}, false
	}
	if !isStaff(auth.User) {
		respondError(w, http.StatusForbidden, "staff access is required")
		return authContext{}, false
	}
	return auth, true
}

func (s *Server) requireBrowserSession(w http.ResponseWriter, auth authContext) bool {
	if auth.ViaToken || strings.TrimSpace(auth.SessionToken) == "" {
		respondError(w, http.StatusForbidden, "browser session is required")
		return false
	}
	return true
}

func (s *Server) requireSessionRequest(w http.ResponseWriter, r *http.Request) bool {
	if !s.requestIsSecure(r) {
		respondError(w, http.StatusBadRequest, "HTTPS is required for browser sessions")
		return false
	}
	if !s.sameOrigin(r) {
		respondError(w, http.StatusForbidden, "same-origin request is required")
		return false
	}
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		respondError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return false
	}
	return true
}

func (s *Server) verifyCSRF(w http.ResponseWriter, r *http.Request, expected string) bool {
	if !s.sameOrigin(r) {
		respondError(w, http.StatusForbidden, "same-origin request is required")
		return false
	}
	token := strings.TrimSpace(r.Header.Get("X-Pappice-CSRF"))
	if token == "" || !security.ConstantTimeEqual(token, expected) {
		respondError(w, http.StatusForbidden, "valid CSRF token is required")
		return false
	}
	return true
}
