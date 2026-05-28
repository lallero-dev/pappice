package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"pappice/internal/security"
)

func (s *Store) CreateIssue(input CreateIssue) (Issue, error) {
	return s.CreateIssueWithAttachments(input, nil, 0)
}

func (s *Store) CreateIssueWithAttachments(input CreateIssue, attachments []CreateAttachment, attachmentUserID int64) (Issue, error) {
	now := time.Now().UTC()
	source := defaultString(input.Source, "staff")
	if !isValid(validIssueSources, source) {
		return Issue{}, fmt.Errorf("%w: invalid issue source %q", ErrValidation, source)
	}
	requesterEmail, err := normalizeEmail(input.RequesterEmail)
	if err != nil {
		return Issue{}, err
	}
	issue := Issue{
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
	if issue.Source == "portal" {
		if issue.RequesterEmail == "" {
			return Issue{}, fmt.Errorf("%w: requester email is required", ErrValidation)
		}
		if issue.RequesterName == "" {
			issue.RequesterName = issue.RequesterEmail
		}
		if issue.Reporter == "" {
			issue.Reporter = issue.RequesterEmail
		}
	}
	if issue.RequesterEmail != "" {
		token, err := security.RandomToken()
		if err != nil {
			return Issue{}, err
		}
		issue.CustomerToken = token
	}
	if issue.ProductID < 1 {
		return Issue{}, fmt.Errorf("%w: product_id is required", ErrValidation)
	}
	if issue.Title == "" {
		return Issue{}, fmt.Errorf("%w: title is required", ErrValidation)
	}
	if !isValid(validSeverities, issue.Severity) {
		return Issue{}, fmt.Errorf("%w: invalid severity %q", ErrValidation, issue.Severity)
	}
	if !isValid(validPriorities, issue.Priority) {
		return Issue{}, fmt.Errorf("%w: invalid priority %q", ErrValidation, issue.Priority)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return Issue{}, err
	}
	defer tx.Rollback()
	if _, err := getProductTx(tx, issue.ProductID); err != nil {
		return Issue{}, err
	}
	if err := tx.QueryRow(`SELECT COALESCE(MAX(number), 0) + 1 FROM issues WHERE product_id = ?`, issue.ProductID).Scan(&issue.Number); err != nil {
		return Issue{}, err
	}
	result, err := tx.Exec(`
		INSERT INTO issues (
			product_id, number, title, description, status, severity, priority, assignee, reporter,
			source, requester_name, requester_email, customer_token, created_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		issue.ProductID, issue.Number, issue.Title, issue.Description, issue.Status, issue.Severity, issue.Priority,
		issue.Assignee, issue.Reporter, issue.Source, issue.RequesterName, issue.RequesterEmail,
		nullEmptyString(issue.CustomerToken), formatTime(issue.CreatedAt), formatTime(issue.UpdatedAt),
	)
	if err != nil {
		return Issue{}, err
	}
	issue.ID, _ = result.LastInsertId()
	if err := insertAttachmentsTx(tx, issue.ID, nil, attachmentUserID, attachments, now); err != nil {
		return Issue{}, err
	}
	if err := tx.Commit(); err != nil {
		return Issue{}, err
	}
	return s.GetIssue(issue.ID)
}

func (s *Store) ListIssues(filter Filter) []Issue {
	return s.listIssues(filter, User{Role: "admin"})
}

func (s *Store) ListIssuesForUser(filter Filter, user User) []Issue {
	return s.listIssues(filter, user)
}

func (s *Store) listIssues(filter Filter, user User) []Issue {
	filter.Query = strings.ToLower(strings.TrimSpace(filter.Query))
	filter.Status = strings.TrimSpace(filter.Status)
	filter.Statuses = normalizeFilterStatuses(filter.Status, filter.Statuses)
	filter.Assignee = strings.TrimSpace(filter.Assignee)

	conditions := []string{"1 = 1"}
	args := []any{}
	role := normalizeGlobalRole(user.Role)
	if role != "admin" {
		staffScope := 0
		if role == "staff" {
			staffScope = 1
		}
		conditions = append(conditions, `EXISTS (
			SELECT 1
			FROM product_members pm
			WHERE pm.product_id = i.product_id
			  AND pm.user_id = ?
			  AND (
			    (? = 1 AND pm.role NOT IN ('customer', 'reporter')) OR
			    lower(i.reporter) = ? OR
			    lower(i.requester_email) = ?
			  )
		)`)
		args = append(args, user.ID, staffScope, strings.ToLower(strings.TrimSpace(user.Username)), strings.ToLower(strings.TrimSpace(user.Email)))
	}
	if filter.ProductID > 0 {
		conditions = append(conditions, "i.product_id = ?")
		args = append(args, filter.ProductID)
	}
	if len(filter.Statuses) == 1 {
		conditions = append(conditions, "i.status = ?")
		args = append(args, filter.Statuses[0])
	} else if len(filter.Statuses) > 1 {
		conditions = append(conditions, fmt.Sprintf("i.status IN (%s)", placeholders(len(filter.Statuses))))
		for _, status := range filter.Statuses {
			args = append(args, status)
		}
	}
	if filter.Assignee != "" {
		conditions = append(conditions, "i.assignee = ?")
		args = append(args, filter.Assignee)
	}
	if filter.Query != "" {
		conditions = append(conditions, `(
			lower(i.title) LIKE ? OR lower(i.description) LIKE ? OR lower(p.key) LIKE ? OR lower(p.name) LIKE ? OR
			lower(i.assignee) LIKE ? OR lower(i.reporter) LIKE ? OR lower(i.requester_name) LIKE ? OR lower(i.requester_email) LIKE ?
		)`)
		q := "%" + filter.Query + "%"
		args = append(args, q, q, q, q, q, q, q, q)
	}

	query := `
		SELECT i.id, i.product_id, p.key, p.name, i.number, i.title, i.description, i.status, i.severity, i.priority,
		       i.assignee, i.reporter, i.source, COALESCE(NULLIF(requester.display_name, ''), NULLIF(i.requester_name, ''), ''), i.requester_email, i.customer_token,
		       i.created_at, i.updated_at, i.closed_at
		FROM issues i
		JOIN products p ON p.id = i.product_id
		LEFT JOIN users requester ON requester.username = lower(i.reporter)
		WHERE ` + strings.Join(conditions, " AND ") + `
		ORDER BY i.updated_at DESC`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var issues []Issue
	for rows.Next() {
		issue, err := scanIssue(rows)
		if err == nil {
			issues = append(issues, issue)
		}
	}
	rows.Close()
	for i := range issues {
		_ = s.hydrateIssue(&issues[i])
	}
	return issues
}

func (s *Store) GetIssue(id int64) (Issue, error) {
	return s.getIssue(id)
}

func (s *Store) GetIssueByKey(key string) (Issue, error) {
	productKey, number, ok := parseIssueKey(key)
	if !ok {
		return Issue{}, ErrNotFound
	}
	row := s.db.QueryRow(issueSelectSQL+` WHERE p.key = ? AND i.number = ?`, productKey, number)
	issue, err := scanIssue(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Issue{}, ErrNotFound
	}
	if err != nil {
		return Issue{}, err
	}
	return issue, s.hydrateIssue(&issue)
}

func (s *Store) GetIssueByCustomerToken(token string) (Issue, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return Issue{}, ErrNotFound
	}
	row := s.db.QueryRow(`
		SELECT i.id, i.product_id, p.key, p.name, i.number, i.title, i.description, i.status, i.severity, i.priority,
		       i.assignee, i.reporter, i.source, COALESCE(NULLIF(requester.display_name, ''), NULLIF(i.requester_name, ''), ''), i.requester_email, i.customer_token,
		       i.created_at, i.updated_at, i.closed_at
		FROM issues i
		JOIN products p ON p.id = i.product_id
		LEFT JOIN users requester ON requester.username = lower(i.reporter)
		WHERE i.customer_token = ?`, token)
	issue, err := scanIssue(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Issue{}, ErrNotFound
	}
	if err != nil {
		return Issue{}, err
	}
	if err := s.hydrateIssue(&issue); err != nil {
		return Issue{}, err
	}
	issue.Comments = publicComments(issue.Comments)
	return issue, nil
}

func (s *Store) UpdateIssue(id int64, patch UpdateIssue) (Issue, error) {
	result, err := s.SaveIssue(SaveIssueInput{IssueID: id, Patch: patch})
	if err != nil {
		return Issue{}, err
	}
	return result.Issue, nil
}

func (s *Store) SaveIssue(input SaveIssueInput) (SaveIssueResult, error) {
	hasPatch := issuePatchPresent(input.Patch)
	hasAttachments := len(input.Attachments) > 0
	hasCommentBody := input.Comment != nil && strings.TrimSpace(input.Comment.Body) != ""
	hasComment := input.Comment != nil && (hasCommentBody || hasAttachments)
	if !hasPatch && input.Comment != nil && !hasComment {
		_, _, err := normalizeComment(*input.Comment, false)
		return SaveIssueResult{}, err
	}
	if !hasPatch && !hasComment && !hasAttachments {
		return SaveIssueResult{}, fmt.Errorf("%w: issue changes or comment are required", ErrValidation)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return SaveIssueResult{}, err
	}
	defer tx.Rollback()

	previous, err := getIssueTx(tx, input.IssueID)
	if err != nil {
		return SaveIssueResult{}, err
	}

	now := time.Now().UTC()
	current := previous
	result := SaveIssueResult{
		Previous:   previous,
		HasPatch:   hasPatch,
		HasComment: hasComment,
	}
	if hasPatch {
		if err := applyIssuePatch(&current, input.Patch, now); err != nil {
			return SaveIssueResult{}, err
		}
		result.AssignmentChanged = input.Patch.Assignee != nil &&
			strings.TrimSpace(*input.Patch.Assignee) != "" &&
			!strings.EqualFold(strings.TrimSpace(*input.Patch.Assignee), strings.TrimSpace(previous.Assignee))
		if err := updateIssueTx(tx, current); err != nil {
			return SaveIssueResult{}, err
		}
	}
	if hasComment {
		comment, publicComment, err := normalizeComment(*input.Comment, hasAttachments)
		if err != nil {
			return SaveIssueResult{}, err
		}
		commentID, err := addCommentTx(tx, input.IssueID, comment, now)
		if err != nil {
			return SaveIssueResult{}, err
		}
		result.CommentID = commentID
		result.PublicComment = publicComment
	}
	if hasAttachments {
		var commentID *int64
		if result.CommentID > 0 {
			commentID = &result.CommentID
		}
		if err := insertAttachmentsTx(tx, input.IssueID, commentID, input.AttachmentUserID, input.Attachments, now); err != nil {
			return SaveIssueResult{}, err
		}
	}
	if !hasPatch && (hasComment || hasAttachments) {
		current.UpdatedAt = now
		if err := updateIssueTimestampTx(tx, input.IssueID, now); err != nil {
			return SaveIssueResult{}, err
		}
	}

	current, err = getIssueTx(tx, input.IssueID)
	if err != nil {
		return SaveIssueResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return SaveIssueResult{}, err
	}
	result.Issue = current
	return result, nil
}

func issuePatchPresent(patch UpdateIssue) bool {
	return patch.Title != nil || patch.Description != nil || patch.Status != nil || patch.Severity != nil || patch.Priority != nil || patch.Assignee != nil
}

func applyIssuePatch(current *Issue, patch UpdateIssue, now time.Time) error {
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

func updateIssueTx(tx *sql.Tx, issue Issue) error {
	_, err := tx.Exec(`
		UPDATE issues
		SET title = ?, description = ?, status = ?, severity = ?, priority = ?, assignee = ?, updated_at = ?, closed_at = ?
		WHERE id = ?`,
		issue.Title, issue.Description, issue.Status, issue.Severity, issue.Priority, issue.Assignee,
		formatTime(issue.UpdatedAt), formatTimePtr(issue.ClosedAt), issue.ID,
	)
	return err
}

func (s *Store) AddComment(id int64, input AddComment) (Issue, error) {
	result, err := s.SaveIssue(SaveIssueInput{IssueID: id, Comment: &input})
	if err != nil {
		return Issue{}, err
	}
	return result.Issue, nil
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
		`INSERT INTO comments (issue_id, author, author_user_id, body, visibility, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
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

func updateIssueTimestampTx(tx *sql.Tx, id int64, now time.Time) error {
	result, err := tx.Exec(`UPDATE issues SET updated_at = ? WHERE id = ?`, formatTime(now), id)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) IssueIDByProductNumber(productID, number int64) (int64, bool) {
	var id int64
	err := s.db.QueryRow(`SELECT id FROM issues WHERE product_id = ? AND number = ?`, productID, number).Scan(&id)
	return id, err == nil
}

func insertAttachmentsTx(tx *sql.Tx, issueID int64, commentID *int64, userID int64, attachments []CreateAttachment, now time.Time) error {
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
				issue_id, comment_id, filename, content_type, size_bytes, sha256, storage_key, created_by_user_id, created_at
			)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			issueID, comment, filename, contentType, attachment.SizeBytes, strings.TrimSpace(attachment.SHA256),
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
		SELECT id, issue_id, comment_id, filename, content_type, size_bytes, sha256, storage_key, created_by_user_id, created_at
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

func (s *Store) getIssue(id int64) (Issue, error) {
	row := s.db.QueryRow(issueSelectSQL+` WHERE i.id = ?`, id)
	issue, err := scanIssue(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Issue{}, ErrNotFound
	}
	if err != nil {
		return Issue{}, err
	}
	return issue, s.hydrateIssue(&issue)
}

func getIssueTx(tx *sql.Tx, id int64) (Issue, error) {
	row := tx.QueryRow(issueSelectSQL+` WHERE i.id = ?`, id)
	issue, err := scanIssue(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Issue{}, ErrNotFound
	}
	if err != nil {
		return Issue{}, err
	}
	return issue, hydrateIssueTx(tx, &issue)
}

func (s *Store) hydrateIssue(issue *Issue) error {
	return hydrateIssueWithQuery(s.db, issue)
}

func hydrateIssueTx(tx *sql.Tx, issue *Issue) error {
	return hydrateIssueWithQuery(tx, issue)
}

type issueQueryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

func hydrateIssueWithQuery(queryer issueQueryer, issue *Issue) error {
	issue.Key = fmt.Sprintf("%s-%d", issue.ProductKey, issue.Number)
	issue.Product = issue.ProductName
	if issue.Product == "" {
		issue.Product = issue.ProductKey
	}
	issue.Attachments = nil
	issue.Comments = nil

	commentRows, err := queryer.Query(`
		SELECT c.id, COALESCE(NULLIF(author_by_id.display_name, ''), NULLIF(author_by_name.display_name, ''), c.author), c.author_user_id, c.body, c.visibility, c.created_at
		FROM comments c
		LEFT JOIN users author_by_id ON author_by_id.id = c.author_user_id
		LEFT JOIN users author_by_name ON c.author_user_id IS NULL AND author_by_name.username = lower(c.author)
		WHERE c.issue_id = ?
		ORDER BY c.created_at`, issue.ID)
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
			issue.Comments = append(issue.Comments, comment)
		}
	}
	if err := commentRows.Err(); err != nil {
		return err
	}

	commentIndexes := make(map[int64]int, len(issue.Comments))
	for i := range issue.Comments {
		commentIndexes[issue.Comments[i].ID] = i
	}
	attachmentRows, err := queryer.Query(`
		SELECT id, issue_id, comment_id, filename, content_type, size_bytes, sha256, storage_key, created_by_user_id, created_at
		FROM attachments
		WHERE issue_id = ?
		ORDER BY created_at, id`, issue.ID)
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
			issue.Attachments = append(issue.Attachments, attachment)
			continue
		}
		if index, ok := commentIndexes[*attachment.CommentID]; ok {
			issue.Comments[index].Attachments = append(issue.Comments[index].Attachments, attachment)
		}
	}
	return attachmentRows.Err()
}

func parseIssueKey(key string) (string, int64, bool) {
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

const issueSelectSQL = `
	SELECT i.id, i.product_id, p.key, p.name, i.number, i.title, i.description, i.status, i.severity, i.priority,
	       i.assignee, i.reporter, i.source, COALESCE(NULLIF(requester.display_name, ''), NULLIF(i.requester_name, ''), ''), i.requester_email, i.customer_token,
	       i.created_at, i.updated_at, i.closed_at
	FROM issues i
	JOIN products p ON p.id = i.product_id
	LEFT JOIN users requester ON requester.username = lower(i.reporter)`

func scanIssue(rows scanner) (Issue, error) {
	var issue Issue
	var closed, customerToken sql.NullString
	var created, updated string
	if err := rows.Scan(
		&issue.ID, &issue.ProductID, &issue.ProductKey, &issue.ProductName, &issue.Number, &issue.Title, &issue.Description,
		&issue.Status, &issue.Severity, &issue.Priority, &issue.Assignee, &issue.Reporter,
		&issue.Source, &issue.RequesterName, &issue.RequesterEmail, &customerToken, &created, &updated, &closed,
	); err != nil {
		return Issue{}, err
	}
	if issue.Source == "" {
		issue.Source = "staff"
	}
	issue.CustomerToken = nullString(customerToken)
	issue.CreatedAt = parseTime(created)
	issue.UpdatedAt = parseTime(updated)
	issue.ClosedAt = parseNullTime(closed)
	issue.Key = fmt.Sprintf("%s-%d", issue.ProductKey, issue.Number)
	issue.Product = issue.ProductName
	if issue.Product == "" {
		issue.Product = issue.ProductKey
	}
	return issue, nil
}

func scanAttachment(rows scanner) (Attachment, error) {
	var attachment Attachment
	var commentID, createdBy sql.NullInt64
	var created string
	if err := rows.Scan(
		&attachment.ID, &attachment.IssueID, &commentID, &attachment.Filename, &attachment.ContentType,
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
