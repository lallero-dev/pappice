package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"pemmece/internal/security"
)

func (s *Store) SetupRequired() bool {
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return true
	}
	return count == 0
}

func (s *Store) CreateFirstAdmin(input CreateUser) (User, error) {
	input.Role = "admin"
	tx, err := s.db.Begin()
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()

	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return User{}, err
	}
	if count > 0 {
		return User{}, fmt.Errorf("%w: setup is already complete", ErrConflict)
	}

	user, err := createUserTx(tx, input)
	if err != nil {
		return User{}, err
	}
	project, err := createProjectTx(tx, CreateProject{Key: "PME", Name: "Inbox"})
	if err != nil {
		return User{}, err
	}
	if _, err := tx.Exec(
		`INSERT INTO project_members (project_id, user_id, role, created_at) VALUES (?, ?, 'owner', ?)`,
		project.ID, user.ID, formatTime(time.Now().UTC()),
	); err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	return publicUserCopy(user), nil
}

func (s *Store) CreateUser(input CreateUser) (User, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	user, err := createUserTx(tx, input)
	if err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	return publicUserCopy(user), nil
}

func (s *Store) ListUsers() []User {
	rows, err := s.db.Query(`SELECT id, username, display_name, email, role, disabled, created_at, updated_at FROM users ORDER BY username`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		user, err := scanUser(rows)
		if err == nil {
			users = append(users, user)
		}
	}
	return users
}

func (s *Store) UpdateUser(id int64, patch UpdateUser) (User, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()

	user, err := getUserTx(tx, id)
	if err != nil {
		return User{}, err
	}
	oldRole := user.Role
	oldDisabled := user.Disabled

	if patch.DisplayName != nil {
		user.DisplayName = defaultString(*patch.DisplayName, user.Username)
	}
	if patch.Email != nil {
		email, err := normalizeEmail(*patch.Email)
		if err != nil {
			return User{}, err
		}
		user.Email = email
	}
	if patch.Password != nil {
		hash, err := security.HashPassword(*patch.Password)
		if err != nil {
			return User{}, fmt.Errorf("%w: %v", ErrValidation, err)
		}
		user.PasswordHash = hash
	}
	if patch.Role != nil {
		role := normalizeGlobalRole(*patch.Role)
		if !isValid(validGlobalRoles, role) {
			return User{}, fmt.Errorf("%w: invalid role %q", ErrValidation, role)
		}
		user.Role = role
	}
	if patch.Disabled != nil {
		user.Disabled = *patch.Disabled
	}
	user.UpdatedAt = time.Now().UTC()

	if _, err := tx.Exec(
		`UPDATE users SET display_name = ?, email = ?, role = ?, password_hash = ?, disabled = ?, updated_at = ? WHERE id = ?`,
		user.DisplayName, nullEmptyString(user.Email), user.Role, user.PasswordHash, boolInt(user.Disabled), formatTime(user.UpdatedAt), user.ID,
	); err != nil {
		return User{}, normalizeSQLError(err)
	}
	if (oldRole == "admin" || !oldDisabled) && !hasActiveAdminTx(tx) {
		return User{}, fmt.Errorf("%w: at least one active admin is required", ErrValidation)
	}
	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	return publicUserCopy(user), nil
}

func (s *Store) DeleteUser(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	user, err := getUserTx(tx, id)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM users WHERE id = ?`, id); err != nil {
		return err
	}
	if user.Role == "admin" && !hasActiveAdminTx(tx) {
		return fmt.Errorf("%w: at least one active admin is required", ErrValidation)
	}
	return tx.Commit()
}

func (s *Store) Authenticate(username, password string) (User, error) {
	username = normalizeUsername(username)
	user, err := s.userByUsername(username, true)
	if err != nil {
		return User{}, fmt.Errorf("%w: invalid username or password", ErrValidation)
	}
	if user.Disabled {
		return User{}, fmt.Errorf("%w: user is disabled", ErrValidation)
	}
	if !security.VerifyPassword(user.PasswordHash, password) {
		return User{}, fmt.Errorf("%w: invalid username or password", ErrValidation)
	}
	return publicUserCopy(user), nil
}

func (s *Store) CreateSession(userID int64) (string, string, time.Time, error) {
	token, err := security.RandomToken()
	if err != nil {
		return "", "", time.Time{}, err
	}
	csrf, err := security.RandomToken()
	if err != nil {
		return "", "", time.Time{}, err
	}
	now := time.Now().UTC()
	expires := now.Add(14 * 24 * time.Hour)

	user, err := s.GetUser(userID)
	if err != nil || user.Disabled {
		return "", "", time.Time{}, ErrNotFound
	}
	_, err = s.db.Exec(
		`INSERT INTO sessions (token_hash, csrf_token, user_id, created_at, expires_at) VALUES (?, ?, ?, ?, ?)`,
		security.HashToken(token), csrf, userID, formatTime(now), formatTime(expires),
	)
	if err != nil {
		return "", "", time.Time{}, err
	}
	return token, csrf, expires, nil
}

func (s *Store) UserBySession(token string) (User, string, bool) {
	hash := security.HashToken(token)
	now := formatTime(time.Now().UTC())
	row := s.db.QueryRow(`
		SELECT u.id, u.username, u.display_name, u.email, u.role, u.disabled, u.created_at, u.updated_at, s.csrf_token
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ? AND s.expires_at > ?`,
		hash, now,
	)
	var user User
	var disabled int
	var email sql.NullString
	var created, updated, csrf string
	if err := row.Scan(&user.ID, &user.Username, &user.DisplayName, &email, &user.Role, &disabled, &created, &updated, &csrf); err != nil {
		return User{}, "", false
	}
	user.Email = nullString(email)
	user.Role = normalizeGlobalRole(user.Role)
	user.Disabled = disabled != 0
	user.CreatedAt = parseTime(created)
	user.UpdatedAt = parseTime(updated)
	if user.Disabled {
		return User{}, "", false
	}
	return publicUserCopy(user), csrf, true
}

func (s *Store) DeleteSession(token string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, security.HashToken(token))
	return err
}

func (s *Store) CreateAPIToken(userID int64, input CreateAPIToken) (PublicAPIToken, string, error) {
	name := defaultString(input.Name, "API token")
	raw, err := security.RandomToken()
	if err != nil {
		return PublicAPIToken{}, "", err
	}
	token := "pme_" + raw
	now := time.Now().UTC()

	user, err := s.GetUser(userID)
	if err != nil || user.Disabled {
		return PublicAPIToken{}, "", ErrNotFound
	}
	result, err := s.db.Exec(
		`INSERT INTO api_tokens (user_id, name, prefix, token_hash, created_at) VALUES (?, ?, ?, ?, ?)`,
		userID, name, token[:12], security.HashToken(token), formatTime(now),
	)
	if err != nil {
		return PublicAPIToken{}, "", err
	}
	id, _ := result.LastInsertId()
	apiToken := APIToken{
		ID:        id,
		UserID:    userID,
		Name:      name,
		Prefix:    token[:12],
		TokenHash: security.HashToken(token),
		CreatedAt: now,
	}
	return publicAPIToken(apiToken), token, nil
}

func (s *Store) ListAPITokens(userID int64) []PublicAPIToken {
	rows, err := s.db.Query(
		`SELECT id, user_id, name, prefix, created_at, last_used_at FROM api_tokens WHERE user_id = ? ORDER BY created_at DESC`,
		userID,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var tokens []PublicAPIToken
	for rows.Next() {
		var token PublicAPIToken
		var created string
		var last sql.NullString
		if err := rows.Scan(&token.ID, &token.UserID, &token.Name, &token.Prefix, &created, &last); err == nil {
			token.CreatedAt = parseTime(created)
			token.LastUsedAt = parseNullTime(last)
			tokens = append(tokens, token)
		}
	}
	return tokens
}

func (s *Store) DeleteAPIToken(userID, tokenID int64) error {
	result, err := s.db.Exec(`DELETE FROM api_tokens WHERE id = ? AND user_id = ?`, tokenID, userID)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) UserByAPIToken(token string) (User, bool) {
	hash := security.HashToken(token)
	row := s.db.QueryRow(`
		SELECT u.id, u.username, u.display_name, u.email, u.role, u.disabled, u.created_at, u.updated_at, t.id, t.last_used_at
		FROM api_tokens t
		JOIN users u ON u.id = t.user_id
		WHERE t.token_hash = ?`,
		hash,
	)
	var user User
	var tokenID int64
	var disabled int
	var email sql.NullString
	var created, updated string
	var last sql.NullString
	if err := row.Scan(&user.ID, &user.Username, &user.DisplayName, &email, &user.Role, &disabled, &created, &updated, &tokenID, &last); err != nil {
		return User{}, false
	}
	user.Email = nullString(email)
	user.Role = normalizeGlobalRole(user.Role)
	user.Disabled = disabled != 0
	user.CreatedAt = parseTime(created)
	user.UpdatedAt = parseTime(updated)
	if user.Disabled {
		return User{}, false
	}
	now := time.Now().UTC()
	lastUsed := parseNullTime(last)
	if lastUsed == nil || now.Sub(*lastUsed) > time.Hour {
		_, _ = s.db.Exec(`UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, formatTime(now), tokenID)
	}
	return publicUserCopy(user), true
}

func (s *Store) GetUser(id int64) (User, error) {
	row := s.db.QueryRow(`SELECT id, username, display_name, email, role, password_hash, disabled, created_at, updated_at FROM users WHERE id = ?`, id)
	var user User
	var disabled int
	var email sql.NullString
	var created, updated string
	if err := row.Scan(&user.ID, &user.Username, &user.DisplayName, &email, &user.Role, &user.PasswordHash, &disabled, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, err
	}
	user.Email = nullString(email)
	user.Role = normalizeGlobalRole(user.Role)
	user.Disabled = disabled != 0
	user.CreatedAt = parseTime(created)
	user.UpdatedAt = parseTime(updated)
	return user, nil
}

func createUserTx(tx *sql.Tx, input CreateUser) (User, error) {
	username := normalizeUsername(input.Username)
	if !usernamePattern.MatchString(username) {
		return User{}, fmt.Errorf("%w: username must be 3-48 lowercase letters, numbers, dot, dash, or underscore", ErrValidation)
	}
	role := normalizeGlobalRole(defaultString(input.Role, "staff"))
	if !isValid(validGlobalRoles, role) {
		return User{}, fmt.Errorf("%w: invalid role %q", ErrValidation, role)
	}
	email, err := normalizeEmail(input.Email)
	if err != nil {
		return User{}, err
	}
	hash, err := security.HashPassword(input.Password)
	if err != nil {
		return User{}, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	now := time.Now().UTC()
	displayName := defaultString(input.DisplayName, username)
	result, err := tx.Exec(
		`INSERT INTO users (username, display_name, email, role, password_hash, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		username, displayName, nullEmptyString(email), role, hash, formatTime(now), formatTime(now),
	)
	if err != nil {
		return User{}, normalizeSQLError(err)
	}
	id, _ := result.LastInsertId()
	return User{
		ID:           id,
		Username:     username,
		DisplayName:  displayName,
		Email:        email,
		Role:         role,
		PasswordHash: hash,
		CreatedAt:    now,
		UpdatedAt:    now,
	}, nil
}

func getUserTx(tx *sql.Tx, id int64) (User, error) {
	row := tx.QueryRow(`SELECT id, username, display_name, email, role, password_hash, disabled, created_at, updated_at FROM users WHERE id = ?`, id)
	var user User
	var disabled int
	var email sql.NullString
	var created, updated string
	if err := row.Scan(&user.ID, &user.Username, &user.DisplayName, &email, &user.Role, &user.PasswordHash, &disabled, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, err
	}
	user.Email = nullString(email)
	user.Role = normalizeGlobalRole(user.Role)
	user.Disabled = disabled != 0
	user.CreatedAt = parseTime(created)
	user.UpdatedAt = parseTime(updated)
	return user, nil
}

func scanUser(rows scanner) (User, error) {
	var user User
	var disabled int
	var email sql.NullString
	var created, updated string
	if err := rows.Scan(&user.ID, &user.Username, &user.DisplayName, &email, &user.Role, &disabled, &created, &updated); err != nil {
		return User{}, err
	}
	user.Email = nullString(email)
	user.Role = normalizeGlobalRole(user.Role)
	user.Disabled = disabled != 0
	user.CreatedAt = parseTime(created)
	user.UpdatedAt = parseTime(updated)
	return user, nil
}

func hasActiveAdminTx(tx *sql.Tx) bool {
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM users WHERE role = 'admin' AND disabled = 0`).Scan(&count); err != nil {
		return false
	}
	return count > 0
}

func (s *Store) userByUsername(username string, includeHash bool) (User, error) {
	row := s.db.QueryRow(`SELECT id, username, display_name, email, role, password_hash, disabled, created_at, updated_at FROM users WHERE username = ?`, username)
	var user User
	var disabled int
	var email sql.NullString
	var created, updated string
	if err := row.Scan(&user.ID, &user.Username, &user.DisplayName, &email, &user.Role, &user.PasswordHash, &disabled, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, err
	}
	user.Email = nullString(email)
	user.Role = normalizeGlobalRole(user.Role)
	user.Disabled = disabled != 0
	user.CreatedAt = parseTime(created)
	user.UpdatedAt = parseTime(updated)
	if !includeHash {
		user.PasswordHash = ""
	}
	return user, nil
}

func publicUserCopy(user User) User {
	user.Role = normalizeGlobalRole(user.Role)
	user.PasswordHash = ""
	return user
}

func publicAPIToken(token APIToken) PublicAPIToken {
	return PublicAPIToken{
		ID:         token.ID,
		UserID:     token.UserID,
		Name:       token.Name,
		Prefix:     token.Prefix,
		CreatedAt:  token.CreatedAt,
		LastUsedAt: token.LastUsedAt,
	}
}
