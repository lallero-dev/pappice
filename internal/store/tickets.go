package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"pappice/internal/security"
)

func (s *Store) CreateTicket(input CreateTicket) (Ticket, error) {
	return s.CreateTicketWithAttachments(input, nil, 0)
}

func (s *Store) CreateTicketWithAttachments(input CreateTicket, attachments []CreateAttachment, attachmentUserID int64) (Ticket, error) {
	now := time.Now().UTC()
	source := defaultString(input.Source, "staff")
	if !isValid(validTicketSources, source) {
		return Ticket{}, fmt.Errorf("%w: invalid ticket source %q", ErrValidation, source)
	}
	requesterEmail, err := normalizeEmail(input.RequesterEmail)
	if err != nil {
		return Ticket{}, err
	}
	ticket := Ticket{
		ProductID:      input.ProductID,
		Title:          strings.TrimSpace(input.Title),
		Description:    strings.TrimSpace(input.Description),
		Status:         "new",
		Severity:       defaultString(input.Severity, "support"),
		Priority:       defaultString(input.Priority, "normal"),
		Assignee:       strings.TrimSpace(input.Assignee),
		Reporter:       strings.TrimSpace(input.Reporter),
		Source:         source,
		RequesterName:  strings.TrimSpace(input.RequesterName),
		RequesterEmail: requesterEmail,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if ticket.Source == "portal" {
		if ticket.RequesterEmail == "" {
			return Ticket{}, fmt.Errorf("%w: requester email is required", ErrValidation)
		}
		if ticket.RequesterName == "" {
			ticket.RequesterName = ticket.RequesterEmail
		}
		if ticket.Reporter == "" {
			ticket.Reporter = ticket.RequesterEmail
		}
	}
	if ticket.RequesterEmail != "" {
		token, err := security.RandomToken()
		if err != nil {
			return Ticket{}, err
		}
		ticket.CustomerToken = token
	}
	if ticket.ProductID < 1 {
		return Ticket{}, fmt.Errorf("%w: product_id is required", ErrValidation)
	}
	if ticket.Title == "" {
		return Ticket{}, fmt.Errorf("%w: title is required", ErrValidation)
	}
	if !isValid(validSeverities, ticket.Severity) {
		return Ticket{}, fmt.Errorf("%w: invalid severity %q", ErrValidation, ticket.Severity)
	}
	if !isValid(validPriorities, ticket.Priority) {
		return Ticket{}, fmt.Errorf("%w: invalid priority %q", ErrValidation, ticket.Priority)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return Ticket{}, err
	}
	defer tx.Rollback()
	if _, err := getProductTx(tx, ticket.ProductID); err != nil {
		return Ticket{}, err
	}
	if err := tx.QueryRow(`SELECT COALESCE(MAX(number), 0) + 1 FROM tickets WHERE product_id = ?`, ticket.ProductID).Scan(&ticket.Number); err != nil {
		return Ticket{}, err
	}
	result, err := tx.Exec(`
		INSERT INTO tickets (
			product_id, number, title, description, status, severity, priority, assignee, reporter,
			source, requester_name, requester_email, customer_token, created_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ticket.ProductID, ticket.Number, ticket.Title, ticket.Description, ticket.Status, ticket.Severity, ticket.Priority,
		ticket.Assignee, ticket.Reporter, ticket.Source, ticket.RequesterName, ticket.RequesterEmail,
		nullEmptyString(ticket.CustomerToken), formatTime(ticket.CreatedAt), formatTime(ticket.UpdatedAt),
	)
	if err != nil {
		return Ticket{}, err
	}
	ticket.ID, _ = result.LastInsertId()
	if err := insertAttachmentsTx(tx, ticket.ID, nil, attachmentUserID, attachments, now); err != nil {
		return Ticket{}, err
	}
	if err := insertTicketCreatedEventTx(tx, ticket, input.Actor, now); err != nil {
		return Ticket{}, err
	}
	if err := tx.Commit(); err != nil {
		return Ticket{}, err
	}
	return s.GetTicket(ticket.ID)
}

func (s *Store) ListTicketSummariesPage(user User, filter TicketSummaryFilter) (TicketSummaryPage, error) {
	filter.Limit, filter.Offset = normalizePage(filter.Limit, filter.Offset, 50, 500)
	query, args := ticketSummaryListQuery(user, filter)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return TicketSummaryPage{}, err
	}
	defer rows.Close()

	page := TicketSummaryPage{
		Tickets: make([]TicketSummary, 0, filter.Limit),
		Limit:   filter.Limit,
		Offset:  filter.Offset,
	}
	for rows.Next() {
		summary, err := scanTicketSummary(rows)
		if err != nil {
			return TicketSummaryPage{}, err
		}
		page.Tickets = append(page.Tickets, summary)
	}
	if err := rows.Err(); err != nil {
		return TicketSummaryPage{}, err
	}
	if len(page.Tickets) > page.Limit {
		page.Tickets = page.Tickets[:page.Limit]
		page.HasMore = true
	}
	return page, nil
}

func (s *Store) TicketSummaryAggregatesForUser(user User, productID int64) (TicketSummaryAggregates, error) {
	base, args := ticketSummarySelect(user, 0, nil)
	statuses := Statuses()
	columns := []string{
		"(SELECT COUNT(*) FROM ticket_summaries WHERE unread_count > 0)",
		"COUNT(*)",
	}
	for range statuses {
		columns = append(columns, "COUNT(*) FILTER (WHERE status = ?)")
	}
	args = append(args, productID, productID)
	for _, status := range statuses {
		args = append(args, status)
	}

	query := `WITH ticket_summaries AS (` + base + `),
		selected AS (SELECT * FROM ticket_summaries WHERE ? = 0 OR product_id = ?)
		SELECT ` + strings.Join(columns, ", ") + ` FROM selected`
	result := TicketSummaryAggregates{Counts: make(map[string]int, len(statuses)+1)}
	all := 0
	destinations := []any{&result.UnreadTotal, &all}
	counts := make([]int, len(statuses))
	for i := range counts {
		destinations = append(destinations, &counts[i])
	}
	if err := s.db.QueryRow(query, args...).Scan(destinations...); err != nil {
		return TicketSummaryAggregates{}, err
	}
	result.Counts["all"] = all
	for i, status := range statuses {
		result.Counts[status] = counts[i]
	}
	return result, nil
}

func (s *Store) TicketSummaryForUser(user User, ticketID int64) (TicketSummary, error) {
	query, args := ticketSummarySelect(user, ticketID, nil)
	summary, err := scanTicketSummary(s.db.QueryRow(query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return TicketSummary{}, ErrNotFound
	}
	return summary, err
}

func (s *Store) GetTicket(id int64) (Ticket, error) {
	return s.getTicket(id)
}

func (s *Store) GetTicketByKey(key string) (Ticket, error) {
	productKey, number, ok := parseTicketKey(key)
	if !ok {
		return Ticket{}, ErrNotFound
	}
	row := s.db.QueryRow(ticketSelectSQL+` WHERE p.key = ? AND i.number = ?`, productKey, number)
	ticket, err := scanTicket(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Ticket{}, ErrNotFound
	}
	if err != nil {
		return Ticket{}, err
	}
	return ticket, s.hydrateTicket(&ticket)
}

func (s *Store) GetTicketByCustomerToken(token string) (Ticket, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return Ticket{}, ErrNotFound
	}
	row := s.db.QueryRow(`
		SELECT i.id, i.product_id, p.key, p.name, i.number, i.title, i.description, i.status, i.severity, i.priority,
		       i.assignee, i.reporter, i.source, COALESCE(NULLIF(requester.display_name, ''), NULLIF(i.requester_name, ''), ''), i.requester_email, i.customer_token,
		       i.created_at, i.updated_at, i.closed_at
		FROM tickets i
		JOIN products p ON p.id = i.product_id
		LEFT JOIN users requester ON lower(requester.email) = lower(i.reporter)
		WHERE i.customer_token = ?`, token)
	ticket, err := scanTicket(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Ticket{}, ErrNotFound
	}
	if err != nil {
		return Ticket{}, err
	}
	if err := s.hydrateTicket(&ticket); err != nil {
		return Ticket{}, err
	}
	ticket.Comments = publicComments(ticket.Comments)
	return ticket, nil
}

func (s *Store) UpdateTicket(id int64, patch UpdateTicket) (Ticket, error) {
	result, err := s.SaveTicket(SaveTicketInput{TicketID: id, Patch: patch})
	if err != nil {
		return Ticket{}, err
	}
	return result.Ticket, nil
}

func (s *Store) SaveTicket(input SaveTicketInput) (SaveTicketResult, error) {
	hasPatch := ticketPatchPresent(input.Patch)
	hasAttachments := len(input.Attachments) > 0
	hasCommentBody := input.Comment != nil && strings.TrimSpace(input.Comment.Body) != ""
	hasComment := input.Comment != nil && (hasCommentBody || hasAttachments)
	if !hasPatch && input.Comment != nil && !hasComment {
		_, _, err := normalizeComment(*input.Comment, false)
		return SaveTicketResult{}, err
	}
	if !hasPatch && !hasComment && !hasAttachments {
		return SaveTicketResult{}, fmt.Errorf("%w: ticket changes or comment are required", ErrValidation)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return SaveTicketResult{}, err
	}
	defer tx.Rollback()

	previous, err := getTicketTx(tx, input.TicketID)
	if err != nil {
		return SaveTicketResult{}, err
	}

	now := time.Now().UTC()
	current := previous
	result := SaveTicketResult{
		Previous:   previous,
		HasPatch:   hasPatch,
		HasComment: hasComment,
	}
	if hasPatch {
		if err := applyTicketPatch(&current, input.Patch, now); err != nil {
			return SaveTicketResult{}, err
		}
		result.AssignmentChanged = input.Patch.Assignee != nil &&
			strings.TrimSpace(*input.Patch.Assignee) != "" &&
			!strings.EqualFold(strings.TrimSpace(*input.Patch.Assignee), strings.TrimSpace(previous.Assignee))
		if err := updateTicketTx(tx, current); err != nil {
			return SaveTicketResult{}, err
		}
	}
	if hasComment {
		comment, publicComment, err := normalizeComment(*input.Comment, hasAttachments)
		if err != nil {
			return SaveTicketResult{}, err
		}
		commentID, err := addCommentTx(tx, input.TicketID, comment, now)
		if err != nil {
			return SaveTicketResult{}, err
		}
		result.CommentID = commentID
		result.PublicComment = publicComment
	}
	if hasAttachments {
		var commentID *int64
		if result.CommentID > 0 {
			commentID = &result.CommentID
		}
		if err := insertAttachmentsTx(tx, input.TicketID, commentID, input.AttachmentUserID, input.Attachments, now); err != nil {
			return SaveTicketResult{}, err
		}
	}
	if !hasPatch && (hasComment || hasAttachments) {
		current.UpdatedAt = now
		if err := updateTicketTimestampTx(tx, input.TicketID, now); err != nil {
			return SaveTicketResult{}, err
		}
	}

	current, err = getTicketTx(tx, input.TicketID)
	if err != nil {
		return SaveTicketResult{}, err
	}
	if err := insertTicketSavedEventsTx(tx, input, result, current, now); err != nil {
		return SaveTicketResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return SaveTicketResult{}, err
	}
	result.Ticket = current
	return result, nil
}

func insertTicketCreatedEventTx(tx *sql.Tx, ticket Ticket, actor EventActor, now time.Time) error {
	payload := ticketEventPayloadJSON(TicketEventPayload{
		Source:               ticket.Source,
		CurrentStatus:        ticket.Status,
		CurrentAssignee:      ticket.Assignee,
		RequesterEmail:       ticket.RequesterEmail,
		RequesterCreatedCopy: ticket.Source == "portal" && strings.TrimSpace(ticket.RequesterEmail) != "",
	})
	_, err := insertDomainEventTx(tx, CreateDomainEvent{
		Type:        "ticket.created",
		ProductID:   ticket.ProductID,
		TicketID:    ticket.ID,
		Actor:       actor,
		PayloadJSON: payload,
	}, now)
	return err
}

func insertTicketSavedEventsTx(tx *sql.Tx, input SaveTicketInput, result SaveTicketResult, current Ticket, now time.Time) error {
	payload := TicketEventPayload{
		HasPatch:          result.HasPatch,
		HasComment:        result.HasComment,
		PublicComment:     result.PublicComment,
		AssignmentChanged: result.AssignmentChanged,
		OnlyAssigneePatch: ticketPatchOnlyAssignee(input.Patch),
		PreviousStatus:    result.Previous.Status,
		CurrentStatus:     current.Status,
		PreviousAssignee:  result.Previous.Assignee,
		CurrentAssignee:   current.Assignee,
		CommentID:         result.CommentID,
		RequesterEmail:    current.RequesterEmail,
		StatusChanged:     !strings.EqualFold(strings.TrimSpace(result.Previous.Status), strings.TrimSpace(current.Status)),
		TerminalStatus:    ticketRequesterTerminalStatus(current.Status),
	}
	if input.Comment != nil {
		payload.CommentVisibility = defaultString(input.Comment.Visibility, "public")
	}
	payloadJSON := ticketEventPayloadJSON(payload)
	if result.HasPatch {
		if _, err := insertDomainEventTx(tx, CreateDomainEvent{
			Type:        "ticket.updated",
			ProductID:   current.ProductID,
			TicketID:    current.ID,
			Actor:       input.Actor,
			PayloadJSON: payloadJSON,
		}, now); err != nil {
			return err
		}
		if result.AssignmentChanged {
			if _, err := insertDomainEventTx(tx, CreateDomainEvent{
				Type:        "ticket.assigned",
				ProductID:   current.ProductID,
				TicketID:    current.ID,
				Actor:       input.Actor,
				PayloadJSON: payloadJSON,
			}, now); err != nil {
				return err
			}
		}
	}
	if result.PublicComment {
		if _, err := insertDomainEventTx(tx, CreateDomainEvent{
			Type:        "ticket.commented",
			ProductID:   current.ProductID,
			TicketID:    current.ID,
			Actor:       input.Actor,
			PayloadJSON: payloadJSON,
		}, now); err != nil {
			return err
		}
	}
	return nil
}

func ticketEventPayloadJSON(payload TicketEventPayload) string {
	data, err := json.Marshal(payload)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func ticketPatchOnlyAssignee(patch UpdateTicket) bool {
	return patch.Assignee != nil && patch.Title == nil && patch.Description == nil && patch.Status == nil && patch.Severity == nil && patch.Priority == nil
}

func ticketRequesterTerminalStatus(status string) bool {
	switch strings.TrimSpace(strings.ToLower(status)) {
	case "resolved", "rejected":
		return true
	default:
		return false
	}
}

func ticketPatchPresent(patch UpdateTicket) bool {
	return patch.Title != nil || patch.Description != nil || patch.Status != nil || patch.Severity != nil || patch.Priority != nil || patch.Assignee != nil
}

func applyTicketPatch(current *Ticket, patch UpdateTicket, now time.Time) error {
	if patch.Title != nil {
		title := strings.TrimSpace(*patch.Title)
		if title == "" {
			return fmt.Errorf("%w: title is required", ErrValidation)
		}
		current.Title = title
	}
	if patch.Description != nil {
		current.Description = strings.TrimSpace(*patch.Description)
	}
	if patch.Status != nil {
		status := strings.TrimSpace(*patch.Status)
		if !isValid(validStatuses, status) {
			return fmt.Errorf("%w: invalid status %q", ErrValidation, status)
		}
		current.Status = status
		if status == "resolved" || status == "rejected" {
			closedAt := now
			current.ClosedAt = &closedAt
		} else {
			current.ClosedAt = nil
		}
	}
	if patch.Severity != nil {
		severity := defaultString(*patch.Severity, "support")
		if !isValid(validSeverities, severity) {
			return fmt.Errorf("%w: invalid severity %q", ErrValidation, severity)
		}
		current.Severity = severity
	}
	if patch.Priority != nil {
		priority := defaultString(*patch.Priority, "normal")
		if !isValid(validPriorities, priority) {
			return fmt.Errorf("%w: invalid priority %q", ErrValidation, priority)
		}
		current.Priority = priority
	}
	if patch.Assignee != nil {
		current.Assignee = strings.TrimSpace(*patch.Assignee)
	}
	current.UpdatedAt = now
	return nil
}

func updateTicketTx(tx *sql.Tx, ticket Ticket) error {
	_, err := tx.Exec(`
		UPDATE tickets
		SET title = ?, description = ?, status = ?, severity = ?, priority = ?, assignee = ?, updated_at = ?, closed_at = ?
		WHERE id = ?`,
		ticket.Title, ticket.Description, ticket.Status, ticket.Severity, ticket.Priority, ticket.Assignee,
		formatTime(ticket.UpdatedAt), formatTimePtr(ticket.ClosedAt), ticket.ID,
	)
	return err
}

func (s *Store) AddComment(id int64, input AddComment) (Ticket, error) {
	result, err := s.SaveTicket(SaveTicketInput{TicketID: id, Comment: &input})
	if err != nil {
		return Ticket{}, err
	}
	return result.Ticket, nil
}

func normalizeComment(input AddComment, allowEmptyBody bool) (AddComment, bool, error) {
	body := strings.TrimSpace(input.Body)
	if body == "" && !allowEmptyBody {
		return AddComment{}, false, fmt.Errorf("%w: comment body is required", ErrValidation)
	}
	author := defaultString(input.Author, "anonymous")
	visibility := defaultString(input.Visibility, "public")
	if !isValid(validCommentVisibility, visibility) {
		return AddComment{}, false, fmt.Errorf("%w: invalid comment visibility %q", ErrValidation, visibility)
	}
	return AddComment{Author: author, AuthorUserID: input.AuthorUserID, Body: body, Visibility: visibility}, visibility == "public", nil
}

func addCommentTx(tx *sql.Tx, id int64, input AddComment, now time.Time) (int64, error) {
	var authorUserID sql.NullInt64
	if input.AuthorUserID > 0 {
		authorUserID = sql.NullInt64{Int64: input.AuthorUserID, Valid: true}
	}
	result, err := tx.Exec(
		`INSERT INTO comments (ticket_id, author, author_user_id, body, visibility, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id, input.Author, authorUserID, input.Body, input.Visibility, formatTime(now),
	)
	if err != nil {
		return 0, normalizeSQLError(err)
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return 0, ErrNotFound
	}
	commentID, _ := result.LastInsertId()
	return commentID, nil
}

func updateTicketTimestampTx(tx *sql.Tx, id int64, now time.Time) error {
	result, err := tx.Exec(`UPDATE tickets SET updated_at = ? WHERE id = ?`, formatTime(now), id)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) TicketIDByProductNumber(productID, number int64) (int64, bool) {
	var id int64
	err := s.db.QueryRow(`SELECT id FROM tickets WHERE product_id = ? AND number = ?`, productID, number).Scan(&id)
	return id, err == nil
}

func insertAttachmentsTx(tx *sql.Tx, ticketID int64, commentID *int64, userID int64, attachments []CreateAttachment, now time.Time) error {
	for _, attachment := range attachments {
		filename := strings.TrimSpace(attachment.Filename)
		if filename == "" {
			return fmt.Errorf("%w: attachment filename is required", ErrValidation)
		}
		contentType := strings.TrimSpace(attachment.ContentType)
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		storageKey := strings.TrimSpace(attachment.StorageKey)
		if storageKey == "" {
			return fmt.Errorf("%w: attachment storage key is required", ErrValidation)
		}
		if attachment.SizeBytes < 0 {
			return fmt.Errorf("%w: invalid attachment size", ErrValidation)
		}
		var comment sql.NullInt64
		if commentID != nil && *commentID > 0 {
			comment = sql.NullInt64{Int64: *commentID, Valid: true}
		}
		var createdBy sql.NullInt64
		if userID > 0 {
			createdBy = sql.NullInt64{Int64: userID, Valid: true}
		}
		_, err := tx.Exec(`
			INSERT INTO attachments (
				ticket_id, comment_id, filename, content_type, size_bytes, sha256, storage_key, created_by_user_id, created_at
			)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			ticketID, comment, filename, contentType, attachment.SizeBytes, strings.TrimSpace(attachment.SHA256),
			storageKey, createdBy, formatTime(now),
		)
		if err != nil {
			return normalizeSQLError(err)
		}
	}
	return nil
}

func (s *Store) GetAttachment(id int64) (Attachment, error) {
	row := s.db.QueryRow(`
		SELECT id, ticket_id, comment_id, filename, content_type, size_bytes, sha256, storage_key, created_by_user_id, created_at
		FROM attachments
		WHERE id = ?`, id)
	attachment, err := scanAttachment(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Attachment{}, ErrNotFound
	}
	if err != nil {
		return Attachment{}, err
	}
	return attachment, nil
}

func (s *Store) DeleteTicket(id int64, event ...EventContext) ([]string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	ticket, err := getTicketTx(tx, id)
	if err != nil {
		return nil, err
	}
	storageKeys, err := ticketAttachmentStorageKeysTx(tx, id)
	if err != nil {
		return nil, err
	}

	result, err := tx.Exec(`DELETE FROM tickets WHERE id = ?`, id)
	if err != nil {
		return nil, normalizeSQLError(err)
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return nil, ErrNotFound
	}

	orphaned, err := orphanedAttachmentStorageKeysTx(tx, storageKeys)
	if err != nil {
		return nil, err
	}
	if err := insertAppEventTx(tx, time.Now().UTC(), firstEventContext(event), "ticket.deleted", "ticket", ticket.ID, ticket.Key, map[string]any{
		"product_id": ticket.ProductID,
		"title":      ticket.Title,
	}, nil); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return orphaned, nil
}

func ticketAttachmentStorageKeysTx(tx *sql.Tx, ticketID int64) ([]string, error) {
	return attachmentStorageKeysTx(tx, `ticket_id = ?`, ticketID)
}

func productAttachmentStorageKeysTx(tx *sql.Tx, productID int64) ([]string, error) {
	return attachmentStorageKeysTx(tx, `ticket_id IN (SELECT id FROM tickets WHERE product_id = ?)`, productID)
}

func attachmentStorageKeysTx(tx *sql.Tx, condition string, args ...any) ([]string, error) {
	rows, err := tx.Query(`
		SELECT DISTINCT storage_key
		FROM attachments
		WHERE storage_key <> '' AND `+condition, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var storageKeys []string
	for rows.Next() {
		var storageKey string
		if err := rows.Scan(&storageKey); err != nil {
			return nil, err
		}
		storageKeys = append(storageKeys, storageKey)
	}
	return storageKeys, rows.Err()
}

func orphanedAttachmentStorageKeysTx(tx *sql.Tx, storageKeys []string) ([]string, error) {
	orphaned := make([]string, 0, len(storageKeys))
	for _, storageKey := range storageKeys {
		var references int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM attachments WHERE storage_key = ?`, storageKey).Scan(&references); err != nil {
			return nil, err
		}
		if references == 0 {
			orphaned = append(orphaned, storageKey)
		}
	}
	return orphaned, nil
}

func (s *Store) getTicket(id int64) (Ticket, error) {
	row := s.db.QueryRow(ticketSelectSQL+` WHERE i.id = ?`, id)
	ticket, err := scanTicket(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Ticket{}, ErrNotFound
	}
	if err != nil {
		return Ticket{}, err
	}
	return ticket, s.hydrateTicket(&ticket)
}

func getTicketTx(tx *sql.Tx, id int64) (Ticket, error) {
	row := tx.QueryRow(ticketSelectSQL+` WHERE i.id = ?`, id)
	ticket, err := scanTicket(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Ticket{}, ErrNotFound
	}
	if err != nil {
		return Ticket{}, err
	}
	return ticket, hydrateTicketTx(tx, &ticket)
}

func (s *Store) hydrateTicket(ticket *Ticket) error {
	return hydrateTicketWithQuery(s.db, ticket)
}

func hydrateTicketTx(tx *sql.Tx, ticket *Ticket) error {
	return hydrateTicketWithQuery(tx, ticket)
}

type ticketQueryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

func hydrateTicketWithQuery(queryer ticketQueryer, ticket *Ticket) error {
	ticket.Key = fmt.Sprintf("%s-%d", ticket.ProductKey, ticket.Number)
	ticket.Product = ticket.ProductName
	if ticket.Product == "" {
		ticket.Product = ticket.ProductKey
	}
	ticket.Attachments = nil
	ticket.Comments = nil

	commentRows, err := queryer.Query(`
		SELECT c.id, COALESCE(NULLIF(author_by_id.display_name, ''), NULLIF(author_by_name.display_name, ''), c.author), c.author_user_id, c.body, c.visibility, c.created_at
		FROM comments c
		LEFT JOIN users author_by_id ON author_by_id.id = c.author_user_id
		LEFT JOIN users author_by_name ON c.author_user_id IS NULL AND lower(author_by_name.email) = lower(c.author)
		WHERE c.ticket_id = ?
		ORDER BY c.created_at`, ticket.ID)
	if err != nil {
		return err
	}
	defer commentRows.Close()
	for commentRows.Next() {
		var comment Comment
		var authorUserID sql.NullInt64
		var created string
		if err := commentRows.Scan(&comment.ID, &comment.Author, &authorUserID, &comment.Body, &comment.Visibility, &created); err == nil {
			if authorUserID.Valid {
				comment.AuthorUserID = authorUserID.Int64
			}
			if comment.Visibility == "" {
				comment.Visibility = "public"
			}
			comment.CreatedAt = parseTime(created)
			ticket.Comments = append(ticket.Comments, comment)
		}
	}
	if err := commentRows.Err(); err != nil {
		return err
	}

	commentIndexes := make(map[int64]int, len(ticket.Comments))
	for i := range ticket.Comments {
		commentIndexes[ticket.Comments[i].ID] = i
	}
	attachmentRows, err := queryer.Query(`
		SELECT id, ticket_id, comment_id, filename, content_type, size_bytes, sha256, storage_key, created_by_user_id, created_at
		FROM attachments
		WHERE ticket_id = ?
		ORDER BY created_at, id`, ticket.ID)
	if err != nil {
		return err
	}
	defer attachmentRows.Close()
	for attachmentRows.Next() {
		attachment, err := scanAttachment(attachmentRows)
		if err != nil {
			return err
		}
		if attachment.CommentID == nil {
			ticket.Attachments = append(ticket.Attachments, attachment)
			continue
		}
		if index, ok := commentIndexes[*attachment.CommentID]; ok {
			ticket.Comments[index].Attachments = append(ticket.Comments[index].Attachments, attachment)
		}
	}
	return attachmentRows.Err()
}

func parseTicketKey(key string) (string, int64, bool) {
	parts := strings.Split(strings.ToUpper(strings.TrimSpace(key)), "-")
	if len(parts) != 2 || !productKeyPattern.MatchString(parts[0]) {
		return "", 0, false
	}
	number, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || number < 1 {
		return "", 0, false
	}
	return parts[0], number, true
}

const ticketSelectSQL = `
	SELECT i.id, i.product_id, p.key, p.name, i.number, i.title, i.description, i.status, i.severity, i.priority,
	       i.assignee, i.reporter, i.source, COALESCE(NULLIF(requester.display_name, ''), NULLIF(i.requester_name, ''), ''), i.requester_email, i.customer_token,
	       i.created_at, i.updated_at, i.closed_at
	FROM tickets i
	JOIN products p ON p.id = i.product_id
	LEFT JOIN users requester ON lower(requester.email) = lower(i.reporter)`

func scanTicket(rows scanner) (Ticket, error) {
	var ticket Ticket
	var closed, customerToken sql.NullString
	var created, updated string
	if err := rows.Scan(
		&ticket.ID, &ticket.ProductID, &ticket.ProductKey, &ticket.ProductName, &ticket.Number, &ticket.Title, &ticket.Description,
		&ticket.Status, &ticket.Severity, &ticket.Priority, &ticket.Assignee, &ticket.Reporter,
		&ticket.Source, &ticket.RequesterName, &ticket.RequesterEmail, &customerToken, &created, &updated, &closed,
	); err != nil {
		return Ticket{}, err
	}
	if ticket.Source == "" {
		ticket.Source = "staff"
	}
	ticket.CustomerToken = nullString(customerToken)
	ticket.CreatedAt = parseTime(created)
	ticket.UpdatedAt = parseTime(updated)
	ticket.ClosedAt = parseNullTime(closed)
	ticket.Key = fmt.Sprintf("%s-%d", ticket.ProductKey, ticket.Number)
	ticket.Product = ticket.ProductName
	if ticket.Product == "" {
		ticket.Product = ticket.ProductKey
	}
	return ticket, nil
}

func ticketSummarySelect(user User, ticketID int64, filter *TicketSummaryFilter) (string, []any) {
	role := normalizeGlobalRole(user.Role)
	email := strings.ToLower(strings.TrimSpace(user.Email))
	identities := compactIdentityValues(user.DisplayName, email, emailLocalPart(email))
	if len(identities) == 0 {
		identities = []string{"\x00"}
	}

	args := make([]any, 0, 24)
	identityMatch := func(expression string) string {
		for _, identity := range identities {
			args = append(args, identity)
		}
		return "lower(trim(" + expression + ")) IN (" + placeholders(len(identities)) + ")"
	}
	requesterName := "COALESCE(NULLIF(requester.display_name, ''), NULLIF(i.requester_name, ''), '')"
	requesterLocal := "CASE WHEN instr(i.requester_email, '@') > 1 THEN substr(i.requester_email, 1, instr(i.requester_email, '@') - 1) ELSE '' END"
	openedByUser := strings.Join([]string{
		identityMatch("i.reporter"),
		identityMatch(requesterName),
		identityMatch("i.requester_email"),
		identityMatch(requesterLocal),
	}, " OR ")
	args = append(args, user.ID)
	commentByUser := "c.author_user_id = ? OR (c.author_user_id IS NULL AND " + identityMatch("c.author") + ")"

	internalComments := "0 = 1"
	if role == "admin" {
		internalComments = "1 = 1"
	} else if role == "staff" {
		internalComments = "pm.role IN ('manager', 'staff')"
	}
	afterRead := func(column string) string {
		return "(tr.last_read_at IS NULL OR " + timestampKeySQL(column) + " > " + timestampKeySQL("tr.last_read_at") + ")"
	}
	unreadCount := `(
		CASE WHEN ` + afterRead("i.created_at") + ` AND NOT (` + openedByUser + `) THEN 1 ELSE 0 END +
		CASE WHEN ` + afterRead("i.updated_at") + ` AND i.status IN ('resolved', 'rejected') THEN 1 ELSE 0 END +
		(SELECT COUNT(*) FROM comments c
		 WHERE c.ticket_id = i.id
		   AND ` + afterRead("c.created_at") + `
		   AND (` + internalComments + ` OR c.visibility = '' OR c.visibility = 'public')
		   AND NOT (` + commentByUser + `))
	)`

	conditions := []string{"1 = 1"}
	args = append(args, user.ID, user.ID)
	if role != "admin" {
		ownTicket := "lower(i.reporter) = ? OR lower(i.requester_email) = ?"
		if role == "staff" {
			conditions = append(conditions, "pm.user_id IS NOT NULL AND (pm.role != 'customer' OR "+ownTicket+")")
		} else {
			conditions = append(conditions, "pm.user_id IS NOT NULL AND ("+ownTicket+")")
		}
		args = append(args, email, email)
	}
	if ticketID > 0 {
		conditions = append(conditions, "i.id = ?")
		args = append(args, ticketID)
	}
	if filter != nil {
		if filter.ProductID > 0 {
			conditions = append(conditions, "i.product_id = ?")
			args = append(args, filter.ProductID)
		}
		if assignee := strings.TrimSpace(filter.Assignee); assignee != "" && role != "customer" {
			condition := "i.assignee = ?"
			if role != "admin" {
				condition = "pm.role != 'customer' AND " + condition
			}
			conditions = append(conditions, condition)
			args = append(args, assignee)
		}
		if search := strings.ToLower(strings.TrimSpace(filter.Query)); search != "" {
			q := "%" + search + "%"
			searches := []string{
				"lower(i.title) LIKE ?", "lower(i.description) LIKE ?", "lower(p.key) LIKE ?",
				"lower(p.name) LIKE ?", "lower(i.reporter) LIKE ?", "lower(" + requesterName + ") LIKE ?",
				"lower(i.requester_email) LIKE ?",
			}
			for range searches {
				args = append(args, q)
			}
			if role != "customer" {
				assigneeSearch := "lower(i.assignee) LIKE ?"
				if role != "admin" {
					assigneeSearch = "(pm.role != 'customer' AND " + assigneeSearch + ")"
				}
				searches = append(searches, assigneeSearch)
				args = append(args, q)
			}
			conditions = append(conditions, "("+strings.Join(searches, " OR ")+")")
		}
	}

	query := `SELECT i.id, i.product_id, p.key AS product_key, p.name AS product_name,
		       i.number, i.title, i.status, i.priority, i.assignee, i.reporter,
		       ` + requesterName + ` AS requester_name, i.requester_email,
		       COALESCE(pm.role, '') AS product_role, tr.last_read_at,
		       ` + unreadCount + ` AS unread_count, i.created_at, i.updated_at
		FROM tickets i
		JOIN products p ON p.id = i.product_id
		LEFT JOIN users requester ON lower(requester.email) = lower(i.reporter)
		LEFT JOIN product_members pm ON pm.product_id = i.product_id AND pm.user_id = ?
		LEFT JOIN ticket_reads tr ON tr.ticket_id = i.id AND tr.user_id = ?
		WHERE ` + strings.Join(conditions, " AND ")
	return query, args
}

func ticketSummaryListQuery(user User, filter TicketSummaryFilter) (string, []any) {
	base, args := ticketSummarySelect(user, 0, &filter)
	conditions := []string{"1 = 1"}
	statuses := compactStrings(filter.Statuses)
	statusMatch := ""
	if len(statuses) > 0 {
		statusMatch = "status IN (" + placeholders(len(statuses)) + ")"
		for _, status := range statuses {
			args = append(args, status)
		}
	}
	switch {
	case filter.UnreadOnly && statusMatch != "":
		conditions = append(conditions, "unread_count > 0", statusMatch)
	case filter.UnreadOnly:
		conditions = append(conditions, "unread_count > 0")
	case filter.IncludeUnreadOutsideStatus && statusMatch != "":
		conditions = append(conditions, "("+statusMatch+" OR unread_count > 0)")
	case statusMatch != "":
		conditions = append(conditions, statusMatch)
	}
	order, orderArgs := ticketSummaryOrder(filter.Sort, filter.Direction)
	args = append(args, orderArgs...)
	args = append(args, filter.Limit+1, filter.Offset)

	return `WITH ticket_summaries AS (` + base + `)
		SELECT * FROM ticket_summaries
		WHERE ` + strings.Join(conditions, " AND ") + `
		ORDER BY ` + order + `
		LIMIT ? OFFSET ?`, args
}

func ticketSummaryOrder(key, direction string) (string, []any) {
	direction = strings.ToUpper(strings.TrimSpace(direction))
	if direction != "ASC" {
		direction = "DESC"
	}
	column := "updated_at"
	var args []any
	switch strings.TrimSpace(key) {
	case "created_at":
		column = "created_at"
	case "priority":
		column, args = enumOrderSQL("priority", Priorities())
	case "status":
		column, args = enumOrderSQL("status", Statuses())
	case "title":
		column = "title COLLATE NOCASE"
	}
	return column + " " + direction + ", updated_at DESC, id ASC", args
}

func enumOrderSQL(column string, values []string) (string, []any) {
	parts := make([]string, 0, len(values)+2)
	parts = append(parts, "CASE "+column)
	args := make([]any, 0, len(values))
	for index, value := range values {
		parts = append(parts, fmt.Sprintf("WHEN ? THEN %d", index))
		args = append(args, value)
	}
	parts = append(parts, fmt.Sprintf("ELSE %d END", len(values)))
	return strings.Join(parts, " "), args
}

func compactStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func timestampKeySQL(column string) string {
	// SQLite date functions lose sub-millisecond precision; normalize UTC RFC3339 text instead.
	fraction := "CASE WHEN substr(" + column + ", 20, 1) = '.' " +
		"THEN substr(" + column + ", 21, instr(" + column + ", 'Z') - 21) ELSE '' END"
	return "substr(" + column + ", 1, 19) || substr(" + fraction + " || '000000000', 1, 9)"
}

func scanTicketSummary(row scanner) (TicketSummary, error) {
	var summary TicketSummary
	var lastRead sql.NullString
	var created, updated string
	if err := row.Scan(
		&summary.ID, &summary.ProductID, &summary.ProductKey, &summary.ProductName, &summary.Number,
		&summary.Title, &summary.Status, &summary.Priority, &summary.Assignee,
		&summary.Reporter, &summary.RequesterName, &summary.RequesterEmail, &summary.ProductRole,
		&lastRead, &summary.UnreadCount, &created, &updated,
	); err != nil {
		return TicketSummary{}, err
	}
	summary.Key = fmt.Sprintf("%s-%d", summary.ProductKey, summary.Number)
	summary.ProductRole = normalizeProductRole(summary.ProductRole)
	summary.LastReadAt = parseNullTime(lastRead)
	summary.HasUnread = summary.UnreadCount > 0
	summary.CreatedAt = parseTime(created)
	summary.UpdatedAt = parseTime(updated)
	return summary, nil
}

func emailLocalPart(email string) string {
	local, _, ok := strings.Cut(strings.TrimSpace(email), "@")
	if !ok {
		return ""
	}
	return local
}

func scanAttachment(rows scanner) (Attachment, error) {
	var attachment Attachment
	var commentID, createdBy sql.NullInt64
	var created string
	if err := rows.Scan(
		&attachment.ID, &attachment.TicketID, &commentID, &attachment.Filename, &attachment.ContentType,
		&attachment.SizeBytes, &attachment.SHA256, &attachment.StorageKey, &createdBy, &created,
	); err != nil {
		return Attachment{}, err
	}
	if commentID.Valid {
		attachment.CommentID = &commentID.Int64
	}
	if createdBy.Valid {
		attachment.CreatedByUserID = createdBy.Int64
	}
	attachment.CreatedAt = parseTime(created)
	return attachment, nil
}

func publicComments(comments []Comment) []Comment {
	result := make([]Comment, 0, len(comments))
	for _, comment := range comments {
		if comment.Visibility == "" || comment.Visibility == "public" {
			comment.Visibility = "public"
			result = append(result, comment)
		}
	}
	return result
}
