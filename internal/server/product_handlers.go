package server

import (
	"errors"
	"net/http"

	"pappice/internal/store"
)

func (s *Server) handleProducts(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		products, err := s.store.ListProducts(auth.User)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		assignees, err := s.store.ListProductAssignees(auth.User)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		requesters, err := s.store.ListProductRequesters(auth.User)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		respondJSON(w, http.StatusOK, map[string]any{
			"products":   products,
			"assignees":  assignees,
			"requesters": requesters,
		})
	case http.MethodPost:
		if !isAdmin(auth.User) {
			respondError(w, http.StatusForbidden, "admin role is required")
			return
		}
		var input store.CreateProduct
		if !decodeJSON(w, r, &input) {
			return
		}
		input.Event = s.eventContext(r, auth.User)
		product, err := s.store.CreateProduct(input)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		s.dispatchEventsSoon()
		respondJSON(w, http.StatusCreated, product)
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleProductByID(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	parts := routeParts(r.URL.Path, "/api/products/")
	if parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	productID, ok := parsePositiveID(w, parts[0], "invalid product id")
	if !ok {
		return
	}
	access, err := s.productAccess(auth.User, productID)
	if err != nil {
		respondStoreError(w, err)
		return
	}
	if len(parts) == 1 {
		s.handleSingleProduct(w, r, auth, productID, access)
		return
	}
	switch parts[1] {
	case "members":
		s.handleProductMembers(w, r, auth, productID, access, parts[2:])
	case "tickets":
		s.handleProductTickets(w, r, auth, productID, access)
	case "webhooks":
		s.handleProductWebhooks(w, r, auth, productID, access)
	case "webhook-deliveries":
		s.handleProductDeliveries(w, r, auth, productID, access)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleSingleProduct(w http.ResponseWriter, r *http.Request, auth authContext, productID int64, access productAccess) {
	switch r.Method {
	case http.MethodGet:
		if !access.read {
			respondError(w, http.StatusNotFound, "not found")
			return
		}
		product, err := s.store.GetProduct(productID)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		product.Role = access.role
		respondJSON(w, http.StatusOK, product)
	case http.MethodPatch:
		if !access.manage {
			respondError(w, http.StatusForbidden, "product manager access is required")
			return
		}
		var patch store.UpdateProduct
		if !decodeJSON(w, r, &patch) {
			return
		}
		patch.Event = s.eventContext(r, auth.User)
		product, err := s.store.UpdateProduct(productID, patch)
		if err != nil {
			respondStoreError(w, err)
			return
		}
		s.dispatchEventsSoon()
		respondJSON(w, http.StatusOK, product)
	case http.MethodDelete:
		if !isAdmin(auth.User) {
			respondError(w, http.StatusForbidden, "admin role is required")
			return
		}
		orphanedStorageKeys, err := s.store.DeleteProduct(productID, s.eventContext(r, auth.User))
		if err != nil {
			respondStoreError(w, err)
			return
		}
		s.removeOrphanedAttachmentFiles(orphanedStorageKeys)
		s.dispatchEventsSoon()
		respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPatch, http.MethodDelete)
	}
}

func (s *Server) handleProductMembers(w http.ResponseWriter, r *http.Request, auth authContext, productID int64, access productAccess, rest []string) {
	if !access.manage {
		respondError(w, http.StatusForbidden, "product manager access is required")
		return
	}
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodGet:
			members, err := s.store.ListProductMembers(productID)
			if err != nil {
				respondStoreError(w, err)
				return
			}
			respondJSON(w, http.StatusOK, map[string]any{"members": members, "roles": store.ProductRoles()})
		case http.MethodPost:
			var input store.UpsertProductMember
			if !decodeJSON(w, r, &input) {
				return
			}
			input.Event = s.eventContext(r, auth.User)
			member, err := s.store.UpsertProductMember(productID, input)
			if err != nil {
				respondStoreError(w, err)
				return
			}
			s.dispatchEventsSoon()
			respondJSON(w, http.StatusCreated, member)
		default:
			methodNotAllowed(w, http.MethodGet, http.MethodPost)
		}
		return
	}
	if len(rest) == 1 && r.Method == http.MethodDelete {
		userID, ok := parsePositiveID(w, rest[0], "invalid user id")
		if !ok {
			return
		}
		if err := s.store.DeleteProductMember(productID, userID, s.eventContext(r, auth.User)); err != nil {
			respondStoreError(w, err)
			return
		}
		s.dispatchEventsSoon()
		respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	http.NotFound(w, r)
}

type productAccess struct {
	role         string
	read         bool
	manage       bool
	createTicket bool
}

func (s *Server) productAccess(user store.User, productID int64) (productAccess, error) {
	if isAdmin(user) {
		return productAccess{role: "manager", read: true, manage: true, createTicket: true}, nil
	}
	role, err := s.store.ProductRole(user.ID, productID)
	if errors.Is(err, store.ErrNotFound) {
		return productAccess{}, nil
	}
	if err != nil {
		return productAccess{}, err
	}
	return productAccess{
		role:         role,
		read:         true,
		manage:       !isCustomer(user) && role == "manager",
		createTicket: role == "manager" || role == "staff" || role == "customer",
	}, nil
}
