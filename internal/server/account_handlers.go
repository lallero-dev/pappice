package server

import (
	"net/http"
	"strings"

	"pappice/internal/store"
)

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		s.handleSession(w, r)
		return
	}
	auth, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPatch {
		methodNotAllowed(w, http.MethodGet, http.MethodPatch)
		return
	}
	if !s.requireBrowserSession(w, auth) {
		return
	}
	var input struct {
		DisplayName *string `json:"display_name"`
		Email       *string `json:"email"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	updated, err := s.store.UpdateUser(auth.User.ID, store.UpdateUser{
		DisplayName: input.DisplayName,
		Email:       input.Email,
		Event:       s.eventContext(r, auth.User),
	})
	if err != nil {
		respondStoreError(w, err)
		return
	}
	s.dispatchEventsSoon()
	respondJSON(w, http.StatusOK, store.ToPublicUser(updated))
}

func (s *Server) handleMePassword(w http.ResponseWriter, r *http.Request) {
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
	var input struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	updated, err := s.store.ChangePassword(auth.User.ID, input.CurrentPassword, input.NewPassword, auth.SessionToken, s.eventContext(r, auth.User))
	if err != nil {
		respondStoreError(w, err)
		return
	}
	s.dispatchEventsSoon()
	respondJSON(w, http.StatusOK, map[string]any{
		"user":       store.ToPublicUser(updated),
		"csrf_token": auth.CSRF,
	})
}

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireStaff(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		users, err := s.store.ListUsers()
		if err != nil {
			respondStoreError(w, err)
			return
		}
		public := make([]store.PublicUser, 0, len(users))
		for _, user := range users {
			public = append(public, store.ToPublicUser(user))
		}
		respondJSON(w, http.StatusOK, map[string]any{"users": public, "roles": store.Roles()})
	case http.MethodPost:
		if !isAdmin(auth.User) {
			respondError(w, http.StatusForbidden, "admin role is required")
			return
		}
		var input store.CreateUser
		if !decodeJSON(w, r, &input) {
			return
		}
		input.Event = s.eventContext(r, auth.User)
		if strings.TrimSpace(input.Password) != "" {
			created, err := s.store.CreateUser(input)
			if err != nil {
				respondStoreError(w, err)
				return
			}
			s.dispatchEventsSoon()
			respondJSON(w, http.StatusCreated, store.ToPublicUser(created))
			return
		}
		created, link, token, err := s.store.CreateUserWithSetupLink(input, accountLinkExpiry)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		queued := s.options.EmailNotifications
		s.dispatchEventsSoon()
		respondJSON(w, http.StatusCreated, userAccountLinkResponse(created, s.accountLinkURL(link.Purpose, token), link.ExpiresAt, queued, s.options.EmailNotifications))
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleUserByID(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	parts := routeParts(r.URL.Path, "/api/users/")
	if parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id, ok := parsePositiveID(w, parts[0], "invalid user id")
	if !ok {
		return
	}
	if len(parts) == 2 && parts[1] == "password-reset" {
		s.handleUserPasswordReset(w, r, auth, id)
		return
	}
	if len(parts) != 1 {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodPatch:
		var patch store.UpdateUser
		if !decodeJSON(w, r, &patch) {
			return
		}
		if patch.Password != nil {
			respondError(w, http.StatusBadRequest, "use password reset")
			return
		}
		patch.Event = s.eventContext(r, auth.User)
		user, err := s.store.UpdateUser(id, patch)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		s.dispatchEventsSoon()
		respondJSON(w, http.StatusOK, store.ToPublicUser(user))
	case http.MethodDelete:
		if err := s.store.DeleteUser(id, s.eventContext(r, auth.User)); err != nil {
			respondStoreError(w, err)
			return
		}
		s.dispatchEventsSoon()
		respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		methodNotAllowed(w, http.MethodPatch, http.MethodDelete)
	}
}

func (s *Server) handleUserPasswordReset(w http.ResponseWriter, r *http.Request, auth authContext, userID int64) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	user, link, token, err := s.store.CreatePasswordResetLink(userID, accountLinkExpiry, s.eventContext(r, auth.User))
	if err != nil {
		respondStoreError(w, err)
		return
	}
	queued := s.options.EmailNotifications
	s.dispatchEventsSoon()
	respondJSON(w, http.StatusCreated, userAccountLinkResponse(user, s.accountLinkURL(link.Purpose, token), link.ExpiresAt, queued, s.options.EmailNotifications))
}

func (s *Server) handleTokens(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireStaff(w, r)
	if !ok {
		return
	}
	if !s.requireBrowserSession(w, auth) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		tokens, err := s.store.ListAPITokens(auth.User.ID)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		respondJSON(w, http.StatusOK, map[string]any{"tokens": tokens})
	case http.MethodPost:
		var input store.CreateAPIToken
		if !decodeJSON(w, r, &input) {
			return
		}
		input.Event = s.eventContext(r, auth.User)
		token, raw, err := s.store.CreateAPIToken(auth.User.ID, input)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		s.dispatchEventsSoon()
		respondJSON(w, http.StatusCreated, map[string]any{"token": token, "value": raw})
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleTokenByID(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireStaff(w, r)
	if !ok {
		return
	}
	if !s.requireBrowserSession(w, auth) {
		return
	}
	id, ok := parseTrailingID(w, r.URL.Path, "/api/tokens/")
	if !ok {
		return
	}
	if r.Method != http.MethodDelete {
		methodNotAllowed(w, http.MethodDelete)
		return
	}
	if err := s.store.DeleteAPIToken(auth.User.ID, id, s.eventContext(r, auth.User)); err != nil {
		respondStoreError(w, err)
		return
	}
	s.dispatchEventsSoon()
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
