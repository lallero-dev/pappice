package server

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"pappice/internal/store"
)

type ticketPatchInput struct {
	Title          *string           `json:"title"`
	Description    *string           `json:"description"`
	Status         *string           `json:"status"`
	Priority       *string           `json:"priority"`
	AssigneeUserID *int64            `json:"assignee_user_id"`
	Comment        *store.AddComment `json:"comment"`
}

func (input ticketPatchInput) updateTicket() store.UpdateTicket {
	return store.UpdateTicket{
		Title:          input.Title,
		Description:    input.Description,
		Status:         input.Status,
		Priority:       input.Priority,
		AssigneeUserID: input.AssigneeUserID,
	}
}

func (input ticketPatchInput) hasTicketPatch() bool {
	return input.Title != nil || input.Description != nil || input.Status != nil || input.Priority != nil || input.AssigneeUserID != nil
}

func (s *Server) handleProductTickets(w http.ResponseWriter, r *http.Request, auth authContext, productID int64, access productAccess) {
	switch r.Method {
	case http.MethodGet:
		if !access.read {
			respondError(w, http.StatusNotFound, "not found")
			return
		}
		query := r.URL.Query()
		limit, offset := paginationParams(r, 50, 500)
		result, err := s.listTicketsForQuery(auth.User, ticketSummaryFilter(query, productID, limit, offset))
		if err != nil {
			respondStoreError(w, err)
			return
		}
		respondJSON(w, http.StatusOK, map[string]any{
			"tickets":      result.Tickets,
			"counts":       result.Counts,
			"unread_total": result.UnreadTotal,
			"limit":        result.Limit,
			"offset":       result.Offset,
			"has_more":     result.HasMore,
			"statuses":     store.Statuses(),
			"priorities":   store.Priorities(),
		})
	case http.MethodPost:
		if !access.createTicket {
			respondError(w, http.StatusForbidden, "product write access is required")
			return
		}
		ticket, ok := s.createTicketFromRequest(w, r, auth, productID)
		if !ok {
			return
		}
		s.dispatchEventsSoon()
		s.respondTicketForUser(w, http.StatusCreated, auth.User, ticket)
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleTickets(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		query := r.URL.Query()
		productID, _ := strconv.ParseInt(query.Get("product_id"), 10, 64)
		limit, offset := paginationParams(r, 50, 500)
		result, err := s.listTicketsForQuery(auth.User, ticketSummaryFilter(query, productID, limit, offset))
		if err != nil {
			respondStoreError(w, err)
			return
		}
		respondJSON(w, http.StatusOK, result)
	case http.MethodPost:
		ticket, ok := s.createTicketFromRequest(w, r, auth, 0)
		if !ok {
			return
		}
		s.dispatchEventsSoon()
		s.respondTicketForUser(w, http.StatusCreated, auth.User, ticket)
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) createTicketFromRequest(w http.ResponseWriter, r *http.Request, auth authContext, fallbackProductID int64) (store.Ticket, bool) {
	var input store.CreateTicket
	multipart := isMultipartRequest(r)
	if multipart {
		if !s.parseMultipartForm(w, r) {
			return store.Ticket{}, false
		}
		defer cleanupMultipartForm(r)
		var err error
		input, err = multipartCreateTicketInput(r, fallbackProductID)
		if err != nil {
			respondStoreError(w, err)
			return store.Ticket{}, false
		}
	} else {
		if !decodeJSON(w, r, &input) {
			return store.Ticket{}, false
		}
		if fallbackProductID > 0 {
			input.ProductID = fallbackProductID
		}
	}

	if fallbackProductID == 0 {
		access, err := s.productAccess(auth.User, input.ProductID)
		if err != nil {
			respondStoreError(w, err)
			return store.Ticket{}, false
		}
		if !access.createTicket {
			respondError(w, http.StatusForbidden, "product write access is required")
			return store.Ticket{}, false
		}
	}
	input.ActorUserID = auth.User.ID

	var uploads []storedUpload
	if multipart {
		var ok bool
		uploads, ok = s.saveRequestAttachments(w, r)
		if !ok {
			return store.Ticket{}, false
		}
	}
	ticket, err := s.store.CreateTicketWithAttachments(input, attachmentInputs(uploads))
	if err != nil {
		cleanupStoredUploads(uploads)
		respondStoreError(w, err)
		return store.Ticket{}, false
	}
	return ticket, true
}

func (s *Server) handleTicketPath(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	parts := routeParts(r.URL.Path, "/api/tickets/")
	if parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	if len(parts) == 2 && parts[0] == "key" {
		s.handleTicketByKey(w, r, auth, parts[1])
		return
	}
	id, ok := parsePositiveID(w, parts[0], "invalid ticket id")
	if !ok {
		return
	}
	ticket, err := s.store.GetTicket(id)
	if err != nil {
		respondStoreError(w, err)
		return
	}
	access, err := s.ticketAccess(auth.User, ticket)
	if err != nil {
		respondStoreError(w, err)
		return
	}
	if !access.read {
		respondError(w, http.StatusNotFound, "not found")
		return
	}

	if len(parts) == 1 {
		s.handleSingleTicket(w, r, auth, ticket, access)
		return
	}
	if len(parts) == 2 && parts[1] == "comments" {
		s.handleComments(w, r, auth, ticket, access)
		return
	}
	if len(parts) == 2 && parts[1] == "read" {
		s.handleTicketRead(w, r, auth, ticket, access)
		return
	}
	http.NotFound(w, r)
}

func (s *Server) handleTicketByKey(w http.ResponseWriter, r *http.Request, auth authContext, key string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	ticket, err := s.store.GetTicketByKey(key)
	if err != nil {
		respondStoreError(w, err)
		return
	}
	access, err := s.ticketAccess(auth.User, ticket)
	if err != nil {
		respondStoreError(w, err)
		return
	}
	if !access.read {
		respondError(w, http.StatusNotFound, "not found")
		return
	}
	s.respondTicket(w, http.StatusOK, auth.User, ticket, access)
}

func (s *Server) handleSingleTicket(w http.ResponseWriter, r *http.Request, auth authContext, ticket store.Ticket, access ticketAccess) {
	switch r.Method {
	case http.MethodGet:
		s.respondTicket(w, http.StatusOK, auth.User, ticket, access)
	case http.MethodPatch:
		var input ticketPatchInput
		var uploads []storedUpload
		if isMultipartRequest(r) {
			if !s.parseMultipartForm(w, r) {
				return
			}
			defer cleanupMultipartForm(r)
			var err error
			input, err = multipartTicketPatchInput(r)
			if err != nil {
				respondStoreError(w, err)
				return
			}
			var ok bool
			uploads, ok = s.saveRequestAttachments(w, r)
			if !ok {
				return
			}
		} else {
			if !decodeJSON(w, r, &input) {
				return
			}
		}
		updated, ok := s.applyTicketPatch(w, auth, ticket, access, input, attachmentInputs(uploads))
		if !ok {
			cleanupStoredUploads(uploads)
			return
		}
		s.respondTicket(w, http.StatusOK, auth.User, updated, access)
	case http.MethodDelete:
		if !isAdmin(auth.User) {
			respondError(w, http.StatusForbidden, "admin role is required")
			return
		}
		orphanedStorageKeys, err := s.store.DeleteTicket(ticket.ID, s.eventContext(r, auth.User))
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

func (s *Server) applyTicketPatch(w http.ResponseWriter, auth authContext, ticket store.Ticket, access ticketAccess, input ticketPatchInput, attachments []store.CreateAttachment) (store.Ticket, bool) {
	hasPatch := input.hasTicketPatch()
	hasAttachments := len(attachments) > 0
	if hasAttachments && input.Comment == nil {
		input.Comment = &store.AddComment{Visibility: "public"}
	}
	hasComment := input.Comment != nil && (strings.TrimSpace(input.Comment.Body) != "" || hasAttachments)
	if !hasPatch && !hasComment {
		respondError(w, http.StatusBadRequest, "ticket changes or comment are required")
		return store.Ticket{}, false
	}
	if hasPatch && !access.edit {
		respondError(w, http.StatusForbidden, "staff access is required")
		return store.Ticket{}, false
	}
	if hasComment && !access.comment {
		respondError(w, http.StatusForbidden, "product comment access is required")
		return store.Ticket{}, false
	}

	var comment *store.AddComment
	if hasComment {
		next := *input.Comment
		next.Visibility = defaultString(next.Visibility, "public")
		if next.Visibility == "internal" && !access.edit {
			respondError(w, http.StatusForbidden, "staff access is required for internal notes")
			return store.Ticket{}, false
		}
		comment = &next
	}

	result, err := s.store.SaveTicket(store.SaveTicketInput{
		TicketID:    ticket.ID,
		Patch:       input.updateTicket(),
		Comment:     comment,
		Attachments: attachments,
		ActorUserID: auth.User.ID,
	})
	if err != nil {
		respondStoreError(w, err)
		return store.Ticket{}, false
	}
	updated := result.Ticket
	s.dispatchEventsSoon()
	return updated, true
}

func (s *Server) handleComments(w http.ResponseWriter, r *http.Request, auth authContext, ticket store.Ticket, access ticketAccess) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if !access.comment {
		respondError(w, http.StatusForbidden, "product comment access is required")
		return
	}
	var input store.AddComment
	var uploads []storedUpload
	if isMultipartRequest(r) {
		if !s.parseMultipartForm(w, r) {
			return
		}
		defer cleanupMultipartForm(r)
		input = multipartCommentInput(r)
		var ok bool
		uploads, ok = s.saveRequestAttachments(w, r)
		if !ok {
			return
		}
	} else {
		if !decodeJSON(w, r, &input) {
			return
		}
	}
	input.Visibility = defaultString(input.Visibility, "public")
	if input.Visibility == "internal" && !access.edit {
		respondError(w, http.StatusForbidden, "staff access is required for internal notes")
		cleanupStoredUploads(uploads)
		return
	}
	updated, ok := s.applyTicketPatch(w, auth, ticket, access, ticketPatchInput{Comment: &input}, attachmentInputs(uploads))
	if !ok {
		cleanupStoredUploads(uploads)
		return
	}
	s.respondTicket(w, http.StatusCreated, auth.User, updated, access)
}

func (s *Server) handleTicketRead(w http.ResponseWriter, r *http.Request, auth authContext, ticket store.Ticket, access ticketAccess) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if err := s.store.MarkTicketRead(ticket.ID, auth.User.ID, time.Now().UTC()); err != nil {
		respondStoreError(w, err)
		return
	}
	updated, err := s.store.GetTicket(ticket.ID)
	if err != nil {
		respondStoreError(w, err)
		return
	}
	s.respondTicket(w, http.StatusOK, auth.User, updated, access)
}

func (s *Server) isSupportTicketRequester(user store.User, ticket store.Ticket) bool {
	return user.ID > 0 && user.ID == ticket.RequesterUserID
}

type ticketAccess struct {
	read         bool
	comment      bool
	edit         bool
	viewAssignee bool
}

func (s *Server) ticketAccess(user store.User, ticket store.Ticket) (ticketAccess, error) {
	product, err := s.productAccess(user, ticket.ProductID)
	if err != nil || !product.read {
		return ticketAccess{}, err
	}
	role := product.role
	requesterOnly := isCustomer(user) || role == "customer"
	return ticketAccess{
		read:         !requesterOnly || s.isSupportTicketRequester(user, ticket),
		comment:      product.createTicket,
		edit:         !isCustomer(user) && (role == "manager" || role == "staff"),
		viewAssignee: !isCustomer(user) && role != "customer",
	}, nil
}

func (s *Server) ticketForUser(user store.User, ticket store.Ticket, access ticketAccess) (store.Ticket, error) {
	if !access.edit {
		ticket.Comments = publicComments(ticket.Comments)
	}
	if !access.viewAssignee {
		ticket.AssigneeUserID = 0
		ticket.AssigneeEmail = ""
	}
	summary, err := s.store.TicketSummaryForUser(user, ticket.ID)
	if err != nil {
		return store.Ticket{}, err
	}
	ticket.UnreadCount = summary.UnreadCount
	ticket.HasUnread = summary.HasUnread
	ticket.LastReadAt = summary.LastReadAt
	return ticket, nil
}

func (s *Server) respondTicket(w http.ResponseWriter, status int, user store.User, ticket store.Ticket, access ticketAccess) {
	ticket, err := s.ticketForUser(user, ticket, access)
	if err != nil {
		respondStoreError(w, err)
		return
	}
	respondJSON(w, status, ticket)
}

func (s *Server) respondTicketForUser(w http.ResponseWriter, status int, user store.User, ticket store.Ticket) {
	access, err := s.ticketAccess(user, ticket)
	if err != nil {
		respondStoreError(w, err)
		return
	}
	s.respondTicket(w, status, user, ticket, access)
}

type ticketListResult struct {
	Tickets     []store.TicketSummary `json:"tickets"`
	Counts      map[string]int        `json:"counts"`
	UnreadTotal int                   `json:"unread_total"`
	Limit       int                   `json:"limit"`
	Offset      int                   `json:"offset"`
	HasMore     bool                  `json:"has_more"`
}

func ticketSummaryFilter(query url.Values, productID int64, limit, offset int) store.TicketSummaryFilter {
	assigneeUserID, _ := strconv.ParseInt(query.Get("assignee_user_id"), 10, 64)
	return store.TicketSummaryFilter{
		Query:                      query.Get("q"),
		Statuses:                   queryStatuses(query),
		ProductID:                  productID,
		AssigneeUserID:             assigneeUserID,
		UnreadOnly:                 queryFlag(query, "unread"),
		IncludeUnreadOutsideStatus: queryFlag(query, "include_unread_outside_status"),
		Sort:                       query.Get("sort"),
		Direction:                  query.Get("direction"),
		Limit:                      limit,
		Offset:                     offset,
	}
}

func (s *Server) listTicketsForQuery(user store.User, filter store.TicketSummaryFilter) (ticketListResult, error) {
	if isCustomer(user) {
		filter.AssigneeUserID = 0
	}
	page, err := s.store.ListTicketSummariesPage(user, filter)
	if err != nil {
		return ticketListResult{}, err
	}
	aggregates, err := s.store.TicketSummaryAggregatesForUser(user, filter.ProductID)
	if err != nil {
		return ticketListResult{}, err
	}
	for i := range page.Tickets {
		if !s.canViewTicketSummaryAssignee(user, page.Tickets[i]) {
			page.Tickets[i].AssigneeUserID = 0
			page.Tickets[i].AssigneeEmail = ""
		}
	}
	return ticketListResult{
		Tickets:     page.Tickets,
		Counts:      aggregates.Counts,
		UnreadTotal: aggregates.UnreadTotal,
		Limit:       page.Limit,
		Offset:      page.Offset,
		HasMore:     page.HasMore,
	}, nil
}

func (s *Server) canViewTicketSummaryAssignee(user store.User, summary store.TicketSummary) bool {
	if isCustomer(user) {
		return false
	}
	return isAdmin(user) || summary.ProductRole != "customer"
}

func publicComments(comments []store.Comment) []store.Comment {
	result := make([]store.Comment, 0, len(comments))
	for _, comment := range comments {
		if comment.Visibility == "" || comment.Visibility == "public" {
			comment.Visibility = "public"
			result = append(result, comment)
		}
	}
	return result
}
