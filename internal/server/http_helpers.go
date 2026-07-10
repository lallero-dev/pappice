package server

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"pappice/internal/store"
)

func queryStatuses(query url.Values) []string {
	seen := make(map[string]struct{}, len(query["status"]))
	statuses := make([]string, 0, len(query["status"]))
	for _, value := range query["status"] {
		for status := range strings.SplitSeq(value, ",") {
			status = strings.TrimSpace(status)
			if status == "" {
				continue
			}
			if _, ok := seen[status]; ok {
				continue
			}
			seen[status] = struct{}{}
			statuses = append(statuses, status)
		}
	}
	return statuses
}

func queryUnread(query url.Values) bool {
	value := strings.ToLower(strings.TrimSpace(query.Get("unread")))
	return value == "1" || value == "true" || value == "yes"
}

func queryIncludeUnreadOutsideStatus(query url.Values) bool {
	value := strings.ToLower(strings.TrimSpace(query.Get("include_unread_outside_status")))
	return value == "1" || value == "true" || value == "yes"
}

func paginationParams(r *http.Request, defaultLimit, maxLimit int) (int, int) {
	query := r.URL.Query()
	limit, err := strconv.Atoi(query.Get("limit"))
	if err != nil || limit < 1 {
		limit = defaultLimit
	}
	limit = min(limit, maxLimit)
	offset, err := strconv.Atoi(query.Get("offset"))
	if err != nil || offset < 0 {
		offset = 0
	}
	return limit, offset
}

func publicWebhooks(hooks []store.Webhook) []store.PublicWebhook {
	public := make([]store.PublicWebhook, 0, len(hooks))
	for _, hook := range hooks {
		public = append(public, store.ToPublicWebhook(hook))
	}
	return public
}

type accountLinkResponse struct {
	URL          string    `json:"url"`
	ExpiresAt    time.Time `json:"expires_at"`
	EmailQueued  bool      `json:"email_queued"`
	EmailEnabled bool      `json:"email_enabled"`
}

type userAccountLinkPayload struct {
	store.PublicUser
	AccountLink accountLinkResponse `json:"account_link"`
}

func userAccountLinkResponse(user store.User, url string, expiresAt time.Time, emailQueued, emailEnabled bool) userAccountLinkPayload {
	return userAccountLinkPayload{
		PublicUser: store.ToPublicUser(user),
		AccountLink: accountLinkResponse{
			URL:          url,
			ExpiresAt:    expiresAt,
			EmailQueued:  emailQueued,
			EmailEnabled: emailEnabled,
		},
	}
}

func (s *Server) ticketURL() string {
	base := strings.TrimRight(s.options.PublicURL, "/")
	if base == "" {
		return "/"
	}
	return base + "/"
}

func (s *Server) accountLinkURL(purpose, token string) string {
	purpose = strings.TrimSpace(purpose)
	token = strings.TrimSpace(token)
	if purpose == "" || token == "" {
		return ""
	}
	base := strings.TrimRight(s.options.PublicURL, "/")
	path := "/account/" + purpose + "/" + token
	if base == "" {
		return path
	}
	return base + path
}

func nullableUser(user store.User, ok bool) any {
	if !ok {
		return nil
	}
	return store.ToPublicUser(user)
}

func nullableString(value string, ok bool) any {
	if !ok {
		return nil
	}
	return value
}

func isAdmin(user store.User) bool {
	return user.Role == "admin"
}

func isStaff(user store.User) bool {
	return user.Role == "admin" || user.Role == "staff"
}

func isCustomer(user store.User) bool {
	return user.Role == "customer"
}

func isUnsafeMethod(method string) bool {
	return method == http.MethodPost || method == http.MethodPatch || method == http.MethodPut || method == http.MethodDelete
}

func (s *Server) sameOrigin(r *http.Request) bool {
	if !s.requestIsSecure(r) {
		return false
	}
	host := s.requestHost(r)
	if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
		return originMatches(origin, host)
	}
	if referer := strings.TrimSpace(r.Header.Get("Referer")); referer != "" {
		return originMatches(referer, host)
	}
	return false
}

func (s *Server) requestIsSecure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return s.options.TrustProxyHeaders && strings.EqualFold(firstForwardedValue(r.Header.Get("X-Forwarded-Proto")), "https")
}

func (s *Server) requestHost(r *http.Request) string {
	if s.options.TrustProxyHeaders {
		if host := firstForwardedValue(r.Header.Get("X-Forwarded-Host")); host != "" {
			return host
		}
	}
	return r.Host
}

func (s *Server) clientIP(r *http.Request) string {
	if s.options.TrustProxyHeaders {
		if ip := strings.TrimSpace(r.Header.Get("X-Real-IP")); ip != "" {
			return ip
		}
		if ip := firstForwardedValue(r.Header.Get("X-Forwarded-For")); ip != "" {
			return ip
		}
	}
	return clientIP(r)
}

func firstForwardedValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if before, _, ok := strings.Cut(value, ","); ok {
		value = before
	}
	return strings.TrimSpace(value)
}

func originMatches(raw, host string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return parsed.Scheme == "https" && strings.EqualFold(parsed.Host, host)
}

func clientIP(r *http.Request) string {
	remoteAddr := strings.TrimSpace(r.RemoteAddr)
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		return host
	}
	return remoteAddr
}

func trimRoutePrefix(path, prefix string) string {
	path, _ = strings.CutPrefix(path, prefix)
	return strings.Trim(path, "/")
}

func routeParts(path, prefix string) []string {
	return strings.Split(trimRoutePrefix(path, prefix), "/")
}

func parsePositiveID(w http.ResponseWriter, raw, message string) (int64, bool) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 1 {
		respondError(w, http.StatusBadRequest, message)
		return 0, false
	}
	return id, true
}

func parseTrailingID(w http.ResponseWriter, path, prefix string) (int64, bool) {
	raw := trimRoutePrefix(path, prefix)
	if raw == "" || strings.Contains(raw, "/") {
		respondError(w, http.StatusNotFound, "not found")
		return 0, false
	}
	return parsePositiveID(w, raw, "invalid id")
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if r.Body == nil {
		respondError(w, http.StatusBadRequest, "request body is required")
		return false
	}
	defer r.Body.Close()

	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		respondError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		respondError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func respondStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		respondError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, store.ErrConflict):
		respondError(w, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrPasswordResetRequired):
		respondError(w, http.StatusUnauthorized, err.Error())
	case errors.Is(err, store.ErrValidation):
		respondError(w, http.StatusBadRequest, err.Error())
	default:
		respondError(w, http.StatusInternalServerError, "internal server error")
	}
}

func respondJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

func respondRateLimited(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "60")
	respondError(w, http.StatusTooManyRequests, "too many attempts; try again later")
}

func methodNotAllowed(w http.ResponseWriter, methods ...string) {
	w.Header().Set("Allow", strings.Join(methods, ", "))
	respondError(w, http.StatusMethodNotAllowed, "method not allowed")
}

func defaultString(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' blob:; object-src 'none'; base-uri 'self'; frame-ancestors 'none'")
		if s.requestIsSecure(r) {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}
