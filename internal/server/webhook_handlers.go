package server

import (
	"encoding/json"
	"net/http"
	"time"

	"pappice/internal/store"
)

func (s *Server) handleWebhooks(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	s.handleWebhookCollection(w, r, auth, nil)
}

func (s *Server) handleProductWebhooks(w http.ResponseWriter, r *http.Request, auth authContext, productID int64, access productAccess) {
	if !access.manage {
		respondError(w, http.StatusForbidden, "product manager access is required")
		return
	}
	s.handleWebhookCollection(w, r, auth, &productID)
}

func (s *Server) handleWebhookCollection(w http.ResponseWriter, r *http.Request, auth authContext, productID *int64) {
	switch r.Method {
	case http.MethodGet:
		hooks, err := s.store.ListWebhooks(productID)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		respondJSON(w, http.StatusOK, map[string]any{
			"webhooks": publicWebhooks(hooks),
			"events":   store.Events(),
		})
	case http.MethodPost:
		var input store.CreateWebhook
		if !decodeJSON(w, r, &input) {
			return
		}
		input.ProductID = productID
		input.Event = s.eventContext(r, auth.User)
		hook, err := s.store.CreateWebhook(input)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		s.dispatchEventsSoon()
		respondJSON(w, http.StatusCreated, map[string]any{"webhook": store.ToPublicWebhook(hook), "secret": hook.Secret})
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleWebhookByID(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireStaff(w, r)
	if !ok {
		return
	}
	parts := routeParts(r.URL.Path, "/api/webhooks/")
	if parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id, ok := parsePositiveID(w, parts[0], "invalid webhook id")
	if !ok {
		return
	}
	hook, err := s.store.GetWebhook(id)
	if err != nil {
		respondStoreError(w, err)
		return
	}
	if hook.ProductID == nil {
		if !isAdmin(auth.User) {
			respondError(w, http.StatusForbidden, "admin role is required")
			return
		}
	} else {
		access, err := s.productAccess(auth.User, *hook.ProductID)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		if !access.manage {
			respondError(w, http.StatusForbidden, "product manager access is required")
			return
		}
	}
	if len(parts) == 2 && parts[1] == "secret" {
		s.handleWebhookSecret(w, r, auth, hook)
		return
	}
	if len(parts) == 2 && parts[1] == "test" {
		s.handleWebhookTest(w, r, auth, hook)
		return
	}
	if len(parts) != 1 {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodPatch:
		var patch store.UpdateWebhook
		if !decodeJSON(w, r, &patch) {
			return
		}
		patch.Event = s.eventContext(r, auth.User)
		updated, err := s.store.UpdateWebhook(id, patch)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		s.dispatchEventsSoon()
		respondJSON(w, http.StatusOK, store.ToPublicWebhook(updated))
	case http.MethodDelete:
		if err := s.store.DeleteWebhook(id, s.eventContext(r, auth.User)); err != nil {
			respondStoreError(w, err)
			return
		}
		s.dispatchEventsSoon()
		respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		methodNotAllowed(w, http.MethodPatch, http.MethodDelete)
	}
}

func (s *Server) handleWebhookSecret(w http.ResponseWriter, r *http.Request, auth authContext, hook store.Webhook) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	updated, secret, err := s.store.RotateWebhookSecret(hook.ID, s.eventContext(r, auth.User))
	if err != nil {
		respondStoreError(w, err)
		return
	}
	s.dispatchEventsSoon()
	respondJSON(w, http.StatusOK, map[string]any{"webhook": store.ToPublicWebhook(updated), "secret": secret})
}

func (s *Server) handleWebhookTest(w http.ResponseWriter, r *http.Request, auth authContext, hook store.Webhook) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	payload := map[string]any{
		"event":      "webhook.test",
		"created_at": time.Now().UTC(),
		"actor":      store.ToPublicUser(auth.User),
		"message":    "Pappice test delivery",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	delivery, err := s.deliverWebhook(hook, "webhook.test", 0, body)
	if err != nil {
		respondStoreError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, delivery)
}

func (s *Server) handleWebhookDeliveries(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	deliveries, err := s.store.ListDeliveries(nil, 50)
	if err != nil {
		respondStoreError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"deliveries": deliveries})
}

func (s *Server) handleProductDeliveries(w http.ResponseWriter, r *http.Request, auth authContext, productID int64, access productAccess) {
	if !access.manage {
		respondError(w, http.StatusForbidden, "product manager access is required")
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	deliveries, err := s.store.ListDeliveries(&productID, 50)
	if err != nil {
		respondStoreError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"deliveries": deliveries})
}
