package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"pemmece/internal/security"
)

func (s *Store) CreateIssue(input CreateIssue) (Issue, error) {
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
		ProjectID:      input.ProjectID,
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
	if issue.ProjectID < 1 {
		return Issue{}, fmt.Errorf("%w: project_id is required", ErrValidation)
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
	if _, err := getProjectTx(tx, issue.ProjectID); err != nil {
		return Issue{}, err
	}
	if err := tx.QueryRow(`SELECT COALESCE(MAX(number), 0) + 1 FROM issues WHERE project_id = ?`, issue.ProjectID).Scan(&issue.Number); err != nil {
		return Issue{}, err
	}
	result, err := tx.Exec(`
		INSERT INTO issues (
			project_id, number, title, description, status, severity, priority, assignee, reporter,
			source, requester_name, requester_email, customer_token, created_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		issue.ProjectID, issue.Number, issue.Title, issue.Description, issue.Status, issue.Severity, issue.Priority,
		issue.Assignee, issue.Reporter, issue.Source, issue.RequesterName, issue.RequesterEmail,
		nullEmptyString(issue.CustomerToken), formatTime(issue.CreatedAt), formatTime(issue.UpdatedAt),
	)
	if err != nil {
		return Issue{}, err
	}
	issue.ID, _ = result.LastInsertId()
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
			FROM project_members pm
			WHERE pm.project_id = i.project_id
			  AND pm.user_id = ?
			  AND (
			    (? = 1 AND pm.role NOT IN ('customer', 'reporter')) OR
			    lower(i.reporter) = ? OR
			    lower(i.requester_email) = ?
			  )
		)`)
		args = append(args, user.ID, staffScope, strings.ToLower(strings.TrimSpace(user.Username)), strings.ToLower(strings.TrimSpace(user.Email)))
	}
	if filter.ProjectID > 0 {
		conditions = append(conditions, "i.project_id = ?")
		args = append(args, filter.ProjectID)
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
		SELECT i.id, i.project_id, p.key, i.number, i.title, i.description, i.status, i.severity, i.priority,
		       i.assignee, i.reporter, i.source, i.requester_name, i.requester_email, i.customer_token,
		       i.created_at, i.updated_at, i.closed_at
		FROM issues i
		JOIN projects p ON p.id = i.project_id
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
	row := s.db.QueryRow(`
		SELECT i.id, i.project_id, p.key, i.number, i.title, i.description, i.status, i.severity, i.priority,
		       i.assignee, i.reporter, i.source, i.requester_name, i.requester_email, i.customer_token,
		       i.created_at, i.updated_at, i.closed_at
		FROM issues i
		JOIN projects p ON p.id = i.project_id
		WHERE i.id = ?`, id)
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
		SELECT i.id, i.project_id, p.key, i.number, i.title, i.description, i.status, i.severity, i.priority,
		       i.assignee, i.reporter, i.source, i.requester_name, i.requester_email, i.customer_token,
		       i.created_at, i.updated_at, i.closed_at
		FROM issues i
		JOIN projects p ON p.id = i.project_id
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
	current, err := s.GetIssue(id)
	if err != nil {
		return Issue{}, err
	}
	if patch.Title != nil {
		title := strings.TrimSpace(*patch.Title)
		if title == "" {
			return Issue{}, fmt.Errorf("%w: title is required", ErrValidation)
		}
		current.Title = title
	}
	if patch.Description != nil {
		current.Description = strings.TrimSpace(*patch.Description)
	}
	if patch.Status != nil {
		status := strings.TrimSpace(*patch.Status)
		if !isValid(validStatuses, status) {
			return Issue{}, fmt.Errorf("%w: invalid status %q", ErrValidation, status)
		}
		current.Status = status
		if status == "resolved" || status == "rejected" {
			now := time.Now().UTC()
			current.ClosedAt = &now
		} else {
			current.ClosedAt = nil
		}
	}
	if patch.Severity != nil {
		severity := defaultString(*patch.Severity, "support")
		if !isValid(validSeverities, severity) {
			return Issue{}, fmt.Errorf("%w: invalid severity %q", ErrValidation, severity)
		}
		current.Severity = severity
	}
	if patch.Priority != nil {
		priority := defaultString(*patch.Priority, "normal")
		if !isValid(validPriorities, priority) {
			return Issue{}, fmt.Errorf("%w: invalid priority %q", ErrValidation, priority)
		}
		current.Priority = priority
	}
	if patch.Assignee != nil {
		current.Assignee = strings.TrimSpace(*patch.Assignee)
	}
	current.UpdatedAt = time.Now().UTC()

	tx, err := s.db.Begin()
	if err != nil {
		return Issue{}, err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`
		UPDATE issues
		SET title = ?, description = ?, status = ?, severity = ?, priority = ?, assignee = ?, updated_at = ?, closed_at = ?
		WHERE id = ?`,
		current.Title, current.Description, current.Status, current.Severity, current.Priority, current.Assignee,
		formatTime(current.UpdatedAt), formatTimePtr(current.ClosedAt), current.ID,
	)
	if err != nil {
		return Issue{}, err
	}
	if err := tx.Commit(); err != nil {
		return Issue{}, err
	}
	return s.GetIssue(id)
}

func (s *Store) AddComment(id int64, input AddComment) (Issue, error) {
	body := strings.TrimSpace(input.Body)
	if body == "" {
		return Issue{}, fmt.Errorf("%w: comment body is required", ErrValidation)
	}
	author := defaultString(input.Author, "anonymous")
	visibility := defaultString(input.Visibility, "public")
	if !isValid(validCommentVisibility, visibility) {
		return Issue{}, fmt.Errorf("%w: invalid comment visibility %q", ErrValidation, visibility)
	}
	now := time.Now().UTC()

	result, err := s.db.Exec(
		`INSERT INTO comments (issue_id, author, body, visibility, created_at) VALUES (?, ?, ?, ?, ?)`,
		id, author, body, visibility, formatTime(now),
	)
	if err != nil {
		return Issue{}, normalizeSQLError(err)
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return Issue{}, ErrNotFound
	}
	_, _ = s.db.Exec(`UPDATE issues SET updated_at = ? WHERE id = ?`, formatTime(now), id)
	return s.GetIssue(id)
}

func (s *Store) IssueIDByProjectNumber(projectID, number int64) (int64, bool) {
	var id int64
	err := s.db.QueryRow(`SELECT id FROM issues WHERE project_id = ? AND number = ?`, projectID, number).Scan(&id)
	return id, err == nil
}

func (s *Store) hydrateIssue(issue *Issue) error {
	issue.Key = fmt.Sprintf("%s-%d", issue.ProjectKey, issue.Number)
	issue.Project = issue.ProjectKey

	commentRows, err := s.db.Query(`SELECT id, author, body, visibility, created_at FROM comments WHERE issue_id = ? ORDER BY created_at`, issue.ID)
	if err != nil {
		return err
	}
	for commentRows.Next() {
		var comment Comment
		var created string
		if err := commentRows.Scan(&comment.ID, &comment.Author, &comment.Body, &comment.Visibility, &created); err == nil {
			if comment.Visibility == "" {
				comment.Visibility = "public"
			}
			comment.CreatedAt = parseTime(created)
			issue.Comments = append(issue.Comments, comment)
		}
	}
	commentRows.Close()

	return nil
}

func scanIssue(rows scanner) (Issue, error) {
	var issue Issue
	var closed, customerToken sql.NullString
	var created, updated string
	if err := rows.Scan(
		&issue.ID, &issue.ProjectID, &issue.ProjectKey, &issue.Number, &issue.Title, &issue.Description,
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
	issue.Key = fmt.Sprintf("%s-%d", issue.ProjectKey, issue.Number)
	issue.Project = issue.ProjectKey
	return issue, nil
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
