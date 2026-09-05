package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

func (s *Store) CreateTicket(input CreateTicket) (Ticket, error) {
	return s.CreateTicketWithAttachments(input, nil)
}

func (s *Store) CreateTicketWithAttachments(input CreateTicket, attachments []CreateAttachment) (Ticket, error) {
	now := time.Now().UTC()
	ticket := Ticket{
		ProductID:      input.ProductID,
		Title:          strings.TrimSpace(input.Title),
		Description:    strings.TrimSpace(input.Description),
		Status:         "open",
		Priority:       defaultString(input.Priority, "normal"),
		AssigneeUserID: input.AssigneeUserID,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if input.ActorUserID < 1 {
		return Ticket{}, fmt.Errorf("%w: actor user is required", ErrValidation)
	}
	if ticket.ProductID < 1 {
		return Ticket{}, fmt.Errorf("%w: product_id is required", ErrValidation)
	}
	if ticket.Title == "" {
		return Ticket{}, fmt.Errorf("%w: title is required", ErrValidation)
	}
	if !slices.Contains(ticketPriorities, ticket.Priority) {
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
	actor, productRole, err := ticketCreatorTx(tx, ticket.ProductID, input.ActorUserID)
	if err != nil {
		return Ticket{}, err
	}
	customerCreator := actor.Role == "customer" || productRole == "customer"
	requester := actor
	if input.RequesterUserID != 0 && input.RequesterUserID != actor.ID {
		if customerCreator {
			return Ticket{}, fmt.Errorf("%w: customers cannot select another requester", ErrValidation)
		}
		requester, err = ticketRequesterTx(tx, ticket.ProductID, input.RequesterUserID)
		if err != nil {
			return Ticket{}, err
		}
	}
	ticket.RequesterUserID = requester.ID
	ticket.CreatedByUserID = actor.ID
	if customerCreator {
		ticket.AssigneeUserID = 0
	}
	ticket.AssigneeEmail, err = ticketAssigneeEmailTx(tx, ticket.ProductID, ticket.AssigneeUserID)
	if err != nil {
		return Ticket{}, err
	}
	if err := tx.QueryRow(`SELECT COALESCE(MAX(number), 0) + 1 FROM tickets WHERE product_id = ?`, ticket.ProductID).Scan(&ticket.Number); err != nil {
		return Ticket{}, err
	}
	result, err := tx.Exec(`
		INSERT INTO tickets (
			product_id, number, title, description, status, priority, assignee_user_id, requester_user_id,
			created_by_user_id, created_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ticket.ProductID, ticket.Number, ticket.Title, ticket.Description, ticket.Status, ticket.Priority,
		nullZero(ticket.AssigneeUserID), ticket.RequesterUserID, actor.ID,
		formatTime(ticket.CreatedAt), formatTime(ticket.UpdatedAt),
	)
	if err != nil {
		return Ticket{}, err
	}
	ticket.ID, err = insertedID(result)
	if err != nil {
		return Ticket{}, err
	}
	if err := insertAttachmentsTx(tx, ticket.ID, nil, actor.ID, attachments, now); err != nil {
		return Ticket{}, err
	}
	if err := insertTicketCreatedEventTx(tx, ticket, EventActorFromUser(actor), now); err != nil {
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

func (s *Store) SaveTicket(input SaveTicketInput) (SaveTicketResult, error) {
	if input.ActorUserID < 1 {
		return SaveTicketResult{}, fmt.Errorf("%w: actor user is required", ErrValidation)
	}
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

	previous, err := getTicketRecordTx(tx, input.TicketID)
	if err != nil {
		return SaveTicketResult{}, err
	}
	actor, err := getUserTx(tx, input.ActorUserID)
	if err != nil {
		return SaveTicketResult{}, err
	}
	if actor.Disabled {
		return SaveTicketResult{}, fmt.Errorf("%w: actor is disabled", ErrValidation)
	}

	now := time.Now().UTC()
	var comment AddComment
	publicComment := false
	if hasComment {
		comment, publicComment, err = normalizeComment(*input.Comment, hasAttachments)
		if err != nil {
			return SaveTicketResult{}, err
		}
		input.Comment = &comment
	}
	replayed, err := recordTicketRequestTx(tx, input)
	if err != nil {
		return SaveTicketResult{}, err
	}
	if replayed {
		current, err := getTicketTx(tx, input.TicketID)
		if err != nil {
			return SaveTicketResult{}, err
		}
		return SaveTicketResult{Ticket: current, Replayed: true}, tx.Commit()
	}
	if publicComment && previous.Status == "closed" {
		status := "open"
		input.Patch.Status = &status
		hasPatch = true
	}
	current := previous
	result := SaveTicketResult{
		Previous:      previous,
		HasPatch:      hasPatch,
		HasComment:    hasComment,
		PublicComment: publicComment,
	}
	if hasPatch {
		if err := applyTicketPatch(&current, input.Patch, now); err != nil {
			return SaveTicketResult{}, err
		}
		statusChanged := current.Status != previous.Status
		if statusChanged || hasComment || hasAttachments {
			current.UpdatedAt = now
		}
		if input.Patch.AssigneeUserID != nil {
			current.AssigneeEmail, err = ticketAssigneeEmailTx(tx, current.ProductID, current.AssigneeUserID)
			if err != nil {
				return SaveTicketResult{}, err
			}
		}
		result.AssignmentChanged = input.Patch.AssigneeUserID != nil &&
			current.AssigneeUserID != previous.AssigneeUserID
		if err := updateTicketTx(tx, current); err != nil {
			return SaveTicketResult{}, err
		}
		if statusChanged {
			if err := addTicketStatusChangeTx(tx, current.ID, actor.ID, previous.Status, current.Status, now); err != nil {
				return SaveTicketResult{}, err
			}
		}
	}
	if hasComment {
		commentID, err := addCommentTx(tx, input.TicketID, comment, actor, now)
		if err != nil {
			return SaveTicketResult{}, err
		}
		result.CommentID = commentID
	}
	if hasAttachments {
		var commentID *int64
		if result.CommentID > 0 {
			commentID = &result.CommentID
		}
		if err := insertAttachmentsTx(tx, input.TicketID, commentID, actor.ID, input.Attachments, now); err != nil {
			return SaveTicketResult{}, err
		}
	}
	if !hasPatch && (hasComment || hasAttachments) {
		current.UpdatedAt = now
		if err := updateTicketTimestampTx(tx, input.TicketID, now); err != nil {
			return SaveTicketResult{}, err
		}
	}
	if err := markTicketRead(tx, input.TicketID, actor.ID, now); err != nil {
		return SaveTicketResult{}, err
	}

	current, err = getTicketTx(tx, input.TicketID)
	if err != nil {
		return SaveTicketResult{}, err
	}
	if err := insertTicketSavedEventsTx(tx, input, result, current, EventActorFromUser(actor), now); err != nil {
		return SaveTicketResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return SaveTicketResult{}, err
	}
	result.Ticket = current
	return result, nil
}

func insertTicketCreatedEventTx(tx *sql.Tx, ticket Ticket, actor EventActor, now time.Time) error {
	payload, err := ticketEventPayloadJSON(TicketEventPayload{
		CurrentStatus:   ticket.Status,
		CurrentAssignee: ticket.AssigneeEmail,
	})
	if err != nil {
		return err
	}
	_, err = insertDomainEventTx(tx, CreateDomainEvent{
		Type:        "ticket.created",
		ProductID:   ticket.ProductID,
		TicketID:    ticket.ID,
		Actor:       actor,
		PayloadJSON: payload,
	}, now)
	return err
}

func insertTicketSavedEventsTx(tx *sql.Tx, input SaveTicketInput, result SaveTicketResult, current Ticket, actor EventActor, now time.Time) error {
	payload := TicketEventPayload{
		HasPatch:          result.HasPatch,
		PublicComment:     result.PublicComment,
		AssignmentChanged: result.AssignmentChanged,
		OnlyAssigneePatch: ticketPatchOnlyAssignee(input.Patch),
		PreviousStatus:    result.Previous.Status,
		CurrentStatus:     current.Status,
		PreviousAssignee:  result.Previous.AssigneeEmail,
		CurrentAssignee:   current.AssigneeEmail,
		CommentID:         result.CommentID,
	}
	if input.Comment != nil {
		payload.CommentVisibility = defaultString(input.Comment.Visibility, "public")
	}
	payloadJSON, err := ticketEventPayloadJSON(payload)
	if err != nil {
		return err
	}
	if result.HasPatch {
		if _, err := insertDomainEventTx(tx, CreateDomainEvent{
			Type:        "ticket.updated",
			ProductID:   current.ProductID,
			TicketID:    current.ID,
			Actor:       actor,
			PayloadJSON: payloadJSON,
		}, now); err != nil {
			return err
		}
		if result.AssignmentChanged {
			if _, err := insertDomainEventTx(tx, CreateDomainEvent{
				Type:        "ticket.assigned",
				ProductID:   current.ProductID,
				TicketID:    current.ID,
				Actor:       actor,
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
			Actor:       actor,
			PayloadJSON: payloadJSON,
		}, now); err != nil {
			return err
		}
	}
	return nil
}

func ticketEventPayloadJSON(payload TicketEventPayload) (string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func ticketPatchOnlyAssignee(patch UpdateTicket) bool {
	return patch.AssigneeUserID != nil && patch.Title == nil && patch.Description == nil && patch.Status == nil && patch.Priority == nil
}

func ticketPatchPresent(patch UpdateTicket) bool {
	return patch.Title != nil || patch.Description != nil || patch.Status != nil || patch.Priority != nil || patch.AssigneeUserID != nil
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
		if !slices.Contains(ticketStatuses, status) {
			return fmt.Errorf("%w: invalid status %q", ErrValidation, status)
		}
		if status != current.Status {
			current.Status = status
			if status == "closed" {
				closedAt := now
				current.ClosedAt = &closedAt
			} else {
				current.ClosedAt = nil
			}
		}
	}
	if patch.Priority != nil {
		priority := defaultString(*patch.Priority, "normal")
		if !slices.Contains(ticketPriorities, priority) {
			return fmt.Errorf("%w: invalid priority %q", ErrValidation, priority)
		}
		current.Priority = priority
	}
	if patch.AssigneeUserID != nil {
		current.AssigneeUserID = *patch.AssigneeUserID
	}
	return nil
}

func updateTicketTx(tx *sql.Tx, ticket Ticket) error {
	_, err := tx.Exec(`
		UPDATE tickets
		SET title = ?, description = ?, status = ?, priority = ?, assignee_user_id = ?, updated_at = ?, closed_at = ?
		WHERE id = ?`,
		ticket.Title, ticket.Description, ticket.Status, ticket.Priority, nullZero(ticket.AssigneeUserID),
		formatTime(ticket.UpdatedAt), formatTimePtr(ticket.ClosedAt), ticket.ID,
	)
	return err
}

func ticketAssigneeEmailTx(tx *sql.Tx, productID, userID int64) (string, error) {
	if userID == 0 {
		return "", nil
	}
	var email string
	err := tx.QueryRow(`
		SELECT u.email
		FROM product_members pm
		JOIN users u ON u.id = pm.user_id
		WHERE pm.product_id = ?
		  AND u.id = ?
		  AND `+productAssigneeEligibilitySQL, productID, userID).Scan(&email)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("%w: assignee must be an active staff member of this product", ErrValidation)
	}
	return email, err
}

func ticketCreatorTx(tx *sql.Tx, productID, userID int64) (User, string, error) {
	user, err := getUserTx(tx, userID)
	if err != nil {
		return User{}, "", err
	}
	if user.Disabled {
		return User{}, "", fmt.Errorf("%w: creator is disabled", ErrValidation)
	}
	if user.Role == "admin" {
		return user, "manager", nil
	}
	var role string
	err = tx.QueryRow(`SELECT role FROM product_members WHERE product_id = ? AND user_id = ?`, productID, userID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, "", fmt.Errorf("%w: creator is not a member of this product", ErrValidation)
	}
	if err != nil {
		return User{}, "", err
	}
	role = normalizeProductRole(role)
	switch role {
	case "manager", "staff", "customer":
		return user, role, nil
	default:
		return User{}, "", fmt.Errorf("%w: product role %q cannot create tickets", ErrValidation, role)
	}
}

func ticketRequesterTx(tx *sql.Tx, productID, userID int64) (User, error) {
	var eligible bool
	err := tx.QueryRow(`
		SELECT EXISTS (
			SELECT 1
			FROM product_members pm
			JOIN users u ON u.id = pm.user_id
			WHERE pm.product_id = ? AND u.id = ? AND `+productRequesterEligibilitySQL+`
		)`, productID, userID).Scan(&eligible)
	if err != nil {
		return User{}, err
	}
	if !eligible {
		return User{}, fmt.Errorf("%w: requester must be an active customer member of this product", ErrValidation)
	}
	return getUserTx(tx, userID)
}

func normalizeComment(input AddComment, allowEmptyBody bool) (AddComment, bool, error) {
	body := strings.TrimSpace(input.Body)
	if body == "" && !allowEmptyBody {
		return AddComment{}, false, fmt.Errorf("%w: comment body is required", ErrValidation)
	}
	visibility := defaultString(input.Visibility, "public")
	if !slices.Contains(commentVisibilities, visibility) {
		return AddComment{}, false, fmt.Errorf("%w: invalid comment visibility %q", ErrValidation, visibility)
	}
	return AddComment{Body: body, Visibility: visibility}, visibility == "public", nil
}

func addCommentTx(tx *sql.Tx, id int64, input AddComment, author User, now time.Time) (int64, error) {
	result, err := tx.Exec(
		`INSERT INTO comments (ticket_id, author, author_user_id, body, visibility, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id, defaultString(author.DisplayName, author.Email), author.ID, input.Body, input.Visibility, formatTime(now),
	)
	if err != nil {
		return 0, normalizeSQLError(err)
	}
	if err := requireChangedRow(result); err != nil {
		return 0, err
	}
	return insertedID(result)
}

func addTicketStatusChangeTx(tx *sql.Tx, ticketID, actorUserID int64, previous, current string, now time.Time) error {
	_, err := tx.Exec(`
		INSERT INTO ticket_status_changes (
			ticket_id, actor_user_id, previous_status, current_status, created_at
		) VALUES (?, ?, ?, ?, ?)`,
		ticketID, actorUserID, previous, current, formatTime(now),
	)
	return err
}

func updateTicketTimestampTx(tx *sql.Tx, id int64, now time.Time) error {
	result, err := tx.Exec(`UPDATE tickets SET updated_at = ? WHERE id = ?`, formatTime(now), id)
	if err != nil {
		return err
	}
	return requireChangedRow(result)
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

func (s *Store) DeleteTicket(id int64, event EventContext) ([]string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	ticket, err := getTicketRecordTx(tx, id)
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
	if err := requireChangedRow(result); err != nil {
		return nil, err
	}

	orphaned, err := orphanedAttachmentStorageKeysTx(tx, storageKeys)
	if err != nil {
		return nil, err
	}
	if err := insertAppEventTx(tx, time.Now().UTC(), event, "ticket.deleted", "ticket", ticket.ID, ticket.Key, map[string]any{
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
	ticket, err := getTicketRecordTx(tx, id)
	if err != nil {
		return Ticket{}, err
	}
	return ticket, hydrateTicketTx(tx, &ticket)
}

func getTicketRecordTx(tx *sql.Tx, id int64) (Ticket, error) {
	row := tx.QueryRow(ticketSelectSQL+` WHERE i.id = ?`, id)
	ticket, err := scanTicket(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Ticket{}, ErrNotFound
	}
	if err != nil {
		return Ticket{}, err
	}
	return ticket, nil
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
	ticket.Attachments = nil
	ticket.Comments = nil
	ticket.StatusChanges = nil

	commentRows, err := queryer.Query(`
		SELECT c.id, COALESCE(NULLIF(author_by_id.display_name, ''), c.author), c.author_user_id, c.body, c.visibility, c.created_at
		FROM comments c
		LEFT JOIN users author_by_id ON author_by_id.id = c.author_user_id
		WHERE c.ticket_id = ?
		ORDER BY c.created_at`, ticket.ID)
	if err != nil {
		return err
	}
	defer commentRows.Close()
	for commentRows.Next() {
		var comment Comment
		var authorUserID sql.NullInt64
		var created dbTime
		if err := commentRows.Scan(&comment.ID, &comment.Author, &authorUserID, &comment.Body, &comment.Visibility, &created); err != nil {
			return err
		}
		if authorUserID.Valid {
			comment.AuthorUserID = authorUserID.Int64
		}
		if comment.Visibility == "" {
			comment.Visibility = "public"
		}
		comment.CreatedAt = created.Time
		ticket.Comments = append(ticket.Comments, comment)
	}
	if err := commentRows.Err(); err != nil {
		return err
	}

	statusRows, err := queryer.Query(`
		SELECT sc.id, sc.actor_user_id, COALESCE(NULLIF(u.display_name, ''), u.email),
		       sc.previous_status, sc.current_status, sc.created_at
		FROM ticket_status_changes sc
		JOIN users u ON u.id = sc.actor_user_id
		WHERE sc.ticket_id = ?
		ORDER BY sc.created_at, sc.id`, ticket.ID)
	if err != nil {
		return err
	}
	defer statusRows.Close()
	for statusRows.Next() {
		var change TicketStatusChange
		var created dbTime
		if err := statusRows.Scan(
			&change.ID, &change.ActorUserID, &change.ActorName,
			&change.PreviousStatus, &change.CurrentStatus, &created,
		); err != nil {
			return err
		}
		change.CreatedAt = created.Time
		ticket.StatusChanges = append(ticket.StatusChanges, change)
	}
	if err := statusRows.Err(); err != nil {
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
	SELECT i.id, i.product_id, p.key, p.name, i.number, i.title, i.description, i.status, i.priority,
	       COALESCE(i.assignee_user_id, 0), COALESCE(assigned_user.email, ''), i.requester_user_id,
	       COALESCE(NULLIF(requester.display_name, ''), requester.email), requester.email, i.created_by_user_id,
	       COALESCE(NULLIF(creator.display_name, ''), creator.email, ''),
	       i.created_at, i.updated_at, i.closed_at
	FROM tickets i
	JOIN products p ON p.id = i.product_id
	LEFT JOIN users assigned_user ON assigned_user.id = i.assignee_user_id
	JOIN users requester ON requester.id = i.requester_user_id
	JOIN users creator ON creator.id = i.created_by_user_id`

func scanTicket(rows scanner) (Ticket, error) {
	var ticket Ticket
	var closed nullDBTime
	var created, updated dbTime
	if err := rows.Scan(
		&ticket.ID, &ticket.ProductID, &ticket.ProductKey, &ticket.ProductName, &ticket.Number, &ticket.Title, &ticket.Description,
		&ticket.Status, &ticket.Priority, &ticket.AssigneeUserID, &ticket.AssigneeEmail, &ticket.RequesterUserID,
		&ticket.RequesterName, &ticket.RequesterEmail, &ticket.CreatedByUserID, &ticket.CreatedByName,
		&created, &updated, &closed,
	); err != nil {
		return Ticket{}, err
	}
	ticket.CreatedAt = created.Time
	ticket.UpdatedAt = updated.Time
	ticket.ClosedAt = closed.Time
	ticket.Key = fmt.Sprintf("%s-%d", ticket.ProductKey, ticket.Number)
	return ticket, nil
}

func ticketSummarySelect(user User, ticketID int64, filter *TicketSummaryFilter) (string, []any) {
	role := normalizeGlobalRole(user.Role)
	args := make([]any, 0, 24)
	requesterName := "COALESCE(NULLIF(requester.display_name, ''), requester.email)"
	requesterEmail := "requester.email"
	openedByUser := "i.created_by_user_id = ?"
	statusByUser := "sc.actor_user_id = ?"
	commentByUser := "c.author_user_id = ?"
	args = append(args, user.ID, user.ID, user.ID)

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
		(SELECT COUNT(*) FROM ticket_status_changes sc
		 WHERE sc.ticket_id = i.id
		   AND ` + afterRead("sc.created_at") + `
		   AND NOT (` + statusByUser + `)) +
		(SELECT COUNT(*) FROM comments c
		 WHERE c.ticket_id = i.id
		   AND ` + afterRead("c.created_at") + `
		   AND (` + internalComments + ` OR c.visibility = '' OR c.visibility = 'public')
		   AND NOT (` + commentByUser + `))
	)`

	conditions := []string{"1 = 1"}
	args = append(args, user.ID, user.ID)
	if role != "admin" {
		ownTicket := "i.requester_user_id = ?"
		if role == "staff" {
			conditions = append(conditions, "pm.user_id IS NOT NULL AND (pm.role != 'customer' OR "+ownTicket+")")
		} else {
			conditions = append(conditions, "pm.user_id IS NOT NULL AND ("+ownTicket+")")
		}
		args = append(args, user.ID)
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
		if filter.AssigneeUserID > 0 && role != "customer" {
			condition := "i.assignee_user_id = ?"
			if role != "admin" {
				condition = "pm.role != 'customer' AND " + condition
			}
			conditions = append(conditions, condition)
			args = append(args, filter.AssigneeUserID)
		}
		if search := strings.ToLower(strings.TrimSpace(filter.Query)); search != "" {
			q := "%" + search + "%"
			searches := []string{
				"lower(i.title) LIKE ?", "lower(i.description) LIKE ?", "lower(p.key) LIKE ?",
				"lower(p.name) LIKE ?", "lower(" + requesterName + ") LIKE ?", "lower(" + requesterEmail + ") LIKE ?",
			}
			for range searches {
				args = append(args, q)
			}
			if role != "customer" {
				assigneeSearch := "(lower(assigned_user.email) LIKE ? OR lower(assigned_user.display_name) LIKE ?)"
				if role != "admin" {
					assigneeSearch = "(pm.role != 'customer' AND " + assigneeSearch + ")"
				}
				searches = append(searches, assigneeSearch)
				args = append(args, q, q)
			}
			conditions = append(conditions, "("+strings.Join(searches, " OR ")+")")
		}
	}

	query := `SELECT i.id, i.product_id, p.key AS product_key, p.name AS product_name,
		       i.number, i.title, i.status, i.priority, COALESCE(i.assignee_user_id, 0), COALESCE(assigned_user.email, ''),
		       i.requester_user_id, ` + requesterName + ` AS requester_name, ` + requesterEmail + ` AS requester_email,
		       COALESCE(pm.role, '') AS product_role, tr.last_read_at,
		       ` + unreadCount + ` AS unread_count, i.created_at, i.updated_at
		FROM tickets i
		JOIN products p ON p.id = i.product_id
		LEFT JOIN users assigned_user ON assigned_user.id = i.assignee_user_id
		JOIN users requester ON requester.id = i.requester_user_id
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
	var lastRead nullDBTime
	var created, updated dbTime
	if err := row.Scan(
		&summary.ID, &summary.ProductID, &summary.ProductKey, &summary.ProductName, &summary.Number,
		&summary.Title, &summary.Status, &summary.Priority, &summary.AssigneeUserID, &summary.AssigneeEmail,
		&summary.RequesterUserID, &summary.RequesterName, &summary.RequesterEmail, &summary.ProductRole,
		&lastRead, &summary.UnreadCount, &created, &updated,
	); err != nil {
		return TicketSummary{}, err
	}
	summary.Key = fmt.Sprintf("%s-%d", summary.ProductKey, summary.Number)
	summary.ProductRole = normalizeProductRole(summary.ProductRole)
	summary.LastReadAt = lastRead.Time
	summary.HasUnread = summary.UnreadCount > 0
	summary.CreatedAt = created.Time
	summary.UpdatedAt = updated.Time
	return summary, nil
}

func scanAttachment(rows scanner) (Attachment, error) {
	var attachment Attachment
	var commentID, createdBy sql.NullInt64
	var created dbTime
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
	attachment.CreatedAt = created.Time
	return attachment, nil
}
