package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
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

func (s *Store) CreateUserWithSetupLink(input CreateUser, expiresFor time.Duration) (User, AccountLink, string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return User{}, AccountLink{}, "", err
	}
	defer tx.Rollback()
	user, err := createPendingUserTx(tx, input)
	if err != nil {
		return User{}, AccountLink{}, "", err
	}
	link, token, err := createAccountLinkTx(tx, user.ID, "setup", expiresFor)
	if err != nil {
		return User{}, AccountLink{}, "", err
	}
	if err := tx.Commit(); err != nil {
		return User{}, AccountLink{}, "", err
	}
	return publicUserCopy(user), link, token, nil
}

func (s *Store) ListUsers() []User {
	rows, err := s.db.Query(`SELECT id, username, display_name, email, role, disabled, password_reset_required, created_at, updated_at FROM users ORDER BY username`)
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
		user.PasswordResetRequired = false
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
		`UPDATE users SET display_name = ?, email = ?, role = ?, password_hash = ?, disabled = ?, password_reset_required = ?, updated_at = ? WHERE id = ?`,
		user.DisplayName, nullEmptyString(user.Email), user.Role, user.PasswordHash, boolInt(user.Disabled),
		boolInt(user.PasswordResetRequired), formatTime(user.UpdatedAt), user.ID,
	); err != nil {
		return User{}, normalizeSQLError(err)
	}
	if patch.Password != nil {
		if _, err := tx.Exec(`DELETE FROM sessions WHERE user_id = ?`, user.ID); err != nil {
			return User{}, err
		}
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
	if user.PasswordResetRequired {
		return User{}, ErrPasswordResetRequired
	}
	if !security.VerifyPassword(user.PasswordHash, password) {
		return User{}, fmt.Errorf("%w: invalid username or password", ErrValidation)
	}
	return publicUserCopy(user), nil
}

func (s *Store) CreateSession(userID int64) (string, string, time.Time, error) {
	return s.CreateSessionFor(userID, 14*24*time.Hour)
}

func (s *Store) CreateSessionFor(userID int64, ttl time.Duration) (string, string, time.Time, error) {
	token, err := security.RandomToken()
	if err != nil {
		return "", "", time.Time{}, err
	}
	csrf, err := security.RandomToken()
	if err != nil {
		return "", "", time.Time{}, err
	}
	if ttl <= 0 {
		ttl = 14 * 24 * time.Hour
	}
	now := time.Now().UTC()
	expires := now.Add(ttl)

	user, err := s.GetUser(userID)
	if err != nil || user.Disabled {
		return "", "", time.Time{}, ErrNotFound
	}
	if user.PasswordResetRequired {
		return "", "", time.Time{}, ErrPasswordResetRequired
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
		SELECT u.id, u.username, u.display_name, u.email, u.role, u.disabled, u.password_reset_required, u.created_at, u.updated_at, s.csrf_token
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ? AND s.expires_at > ?`,
		hash, now,
	)
	var user User
	var disabled int
	var resetRequired int
	var email sql.NullString
	var created, updated, csrf string
	if err := row.Scan(&user.ID, &user.Username, &user.DisplayName, &email, &user.Role, &disabled, &resetRequired, &created, &updated, &csrf); err != nil {
		return User{}, "", false
	}
	user.Email = nullString(email)
	user.Role = normalizeGlobalRole(user.Role)
	user.Disabled = disabled != 0
	user.PasswordResetRequired = resetRequired != 0
	user.CreatedAt = parseTime(created)
	user.UpdatedAt = parseTime(updated)
	if user.Disabled || user.PasswordResetRequired {
		return User{}, "", false
	}
	return publicUserCopy(user), csrf, true
}

func (s *Store) DeleteSession(token string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, security.HashToken(token))
	return err
}

func (s *Store) DeleteUserSessions(userID int64, keepToken string) error {
	if strings.TrimSpace(keepToken) == "" {
		_, err := s.db.Exec(`DELETE FROM sessions WHERE user_id = ?`, userID)
		return err
	}
	_, err := s.db.Exec(`DELETE FROM sessions WHERE user_id = ? AND token_hash <> ?`, userID, security.HashToken(keepToken))
	return err
}

func (s *Store) ChangePassword(userID int64, currentPassword, newPassword, keepSessionToken string) (User, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()

	user, err := getUserTx(tx, userID)
	if err != nil {
		return User{}, err
	}
	if user.Disabled {
		return User{}, ErrNotFound
	}
	if !security.VerifyPassword(user.PasswordHash, currentPassword) {
		return User{}, fmt.Errorf("%w: invalid current password", ErrValidation)
	}
	hash, err := security.HashPassword(newPassword)
	if err != nil {
		return User{}, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	now := time.Now().UTC()
	if _, err := tx.Exec(
		`UPDATE users SET password_hash = ?, password_reset_required = 0, updated_at = ? WHERE id = ?`,
		hash, formatTime(now), userID,
	); err != nil {
		return User{}, err
	}
	if strings.TrimSpace(keepSessionToken) == "" {
		if _, err := tx.Exec(`DELETE FROM sessions WHERE user_id = ?`, userID); err != nil {
			return User{}, err
		}
	} else if _, err := tx.Exec(`DELETE FROM sessions WHERE user_id = ? AND token_hash <> ?`, userID, security.HashToken(keepSessionToken)); err != nil {
		return User{}, err
	}
	user.PasswordHash = hash
	user.PasswordResetRequired = false
	user.UpdatedAt = now
	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	return publicUserCopy(user), nil
}

func (s *Store) CreatePasswordResetLink(userID int64, expiresFor time.Duration) (User, AccountLink, string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return User{}, AccountLink{}, "", err
	}
	defer tx.Rollback()
	user, err := getUserTx(tx, userID)
	if err != nil {
		return User{}, AccountLink{}, "", err
	}
	if user.Disabled {
		return User{}, AccountLink{}, "", ErrNotFound
	}
	link, token, err := createAccountLinkTx(tx, userID, "reset", expiresFor)
	if err != nil {
		return User{}, AccountLink{}, "", err
	}
	now := time.Now().UTC()
	if _, err := tx.Exec(`UPDATE users SET password_reset_required = 1, updated_at = ? WHERE id = ?`, formatTime(now), userID); err != nil {
		return User{}, AccountLink{}, "", err
	}
	if _, err := tx.Exec(`DELETE FROM sessions WHERE user_id = ?`, userID); err != nil {
		return User{}, AccountLink{}, "", err
	}
	user.PasswordResetRequired = true
	user.UpdatedAt = now
	if err := tx.Commit(); err != nil {
		return User{}, AccountLink{}, "", err
	}
	return publicUserCopy(user), link, token, nil
}

func (s *Store) GetAccountLink(token string) (AccountLink, User, error) {
	link, user, err := s.accountLinkByToken(token)
	if err != nil {
		return AccountLink{}, User{}, err
	}
	return link, publicUserCopy(user), nil
}

func (s *Store) AccountLinkStatus(token string) (AccountLinkStatus, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return AccountLinkStatus{}, ErrNotFound
	}
	row := s.db.QueryRow(`
		SELECT al.purpose, al.expires_at, al.used_at, u.disabled
		FROM account_links al
		JOIN users u ON u.id = al.user_id
		WHERE al.token_hash = ?`, security.HashToken(token))
	var status AccountLinkStatus
	var expires string
	var used sql.NullString
	var disabled int
	if err := row.Scan(&status.Purpose, &expires, &used, &disabled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AccountLinkStatus{}, ErrNotFound
		}
		return AccountLinkStatus{}, err
	}
	status.ExpiresAt = parseTime(expires)
	status.UsedAt = parseNullTime(used)
	status.UserDisabled = disabled != 0
	return status, nil
}

func (s *Store) ConsumeAccountLink(token, password string) (User, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()

	link, user, err := accountLinkByTokenTx(tx, token)
	if err != nil {
		return User{}, err
	}
	if user.Disabled {
		return User{}, ErrNotFound
	}
	hash, err := security.HashPassword(password)
	if err != nil {
		return User{}, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	now := time.Now().UTC()
	if _, err := tx.Exec(
		`UPDATE users SET password_hash = ?, password_reset_required = 0, updated_at = ? WHERE id = ?`,
		hash, formatTime(now), link.UserID,
	); err != nil {
		return User{}, err
	}
	if _, err := tx.Exec(`UPDATE account_links SET used_at = ? WHERE id = ?`, formatTime(now), link.ID); err != nil {
		return User{}, err
	}
	if _, err := tx.Exec(`DELETE FROM sessions WHERE user_id = ?`, link.UserID); err != nil {
		return User{}, err
	}
	user.PasswordHash = hash
	user.PasswordResetRequired = false
	user.UpdatedAt = now
	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	return publicUserCopy(user), nil
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
		SELECT u.id, u.username, u.display_name, u.email, u.role, u.disabled, u.password_reset_required, u.created_at, u.updated_at, t.id, t.last_used_at
		FROM api_tokens t
		JOIN users u ON u.id = t.user_id
		WHERE t.token_hash = ?`,
		hash,
	)
	var user User
	var tokenID int64
	var disabled int
	var resetRequired int
	var email sql.NullString
	var created, updated string
	var last sql.NullString
	if err := row.Scan(&user.ID, &user.Username, &user.DisplayName, &email, &user.Role, &disabled, &resetRequired, &created, &updated, &tokenID, &last); err != nil {
		return User{}, false
	}
	user.Email = nullString(email)
	user.Role = normalizeGlobalRole(user.Role)
	user.Disabled = disabled != 0
	user.PasswordResetRequired = resetRequired != 0
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
	row := s.db.QueryRow(`SELECT id, username, display_name, email, role, password_hash, disabled, password_reset_required, created_at, updated_at FROM users WHERE id = ?`, id)
	var user User
	var disabled int
	var resetRequired int
	var email sql.NullString
	var created, updated string
	if err := row.Scan(&user.ID, &user.Username, &user.DisplayName, &email, &user.Role, &user.PasswordHash, &disabled, &resetRequired, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, err
	}
	user.Email = nullString(email)
	user.Role = normalizeGlobalRole(user.Role)
	user.Disabled = disabled != 0
	user.PasswordResetRequired = resetRequired != 0
	user.CreatedAt = parseTime(created)
	user.UpdatedAt = parseTime(updated)
	return user, nil
}

func createUserTx(tx *sql.Tx, input CreateUser) (User, error) {
	username, displayName, email, role, err := normalizeCreateUserInput(input)
	if err != nil {
		return User{}, err
	}
	hash, err := security.HashPassword(input.Password)
	if err != nil {
		return User{}, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	return insertUserTx(tx, username, displayName, email, role, hash, false)
}

func createPendingUserTx(tx *sql.Tx, input CreateUser) (User, error) {
	username, displayName, email, role, err := normalizeCreateUserInput(input)
	if err != nil {
		return User{}, err
	}
	hash, err := unusablePasswordHash()
	if err != nil {
		return User{}, err
	}
	return insertUserTx(tx, username, displayName, email, role, hash, true)
}

func normalizeCreateUserInput(input CreateUser) (string, string, string, string, error) {
	username := normalizeUsername(input.Username)
	if !usernamePattern.MatchString(username) {
		return "", "", "", "", fmt.Errorf("%w: username must be 3-48 lowercase letters, numbers, dot, dash, or underscore", ErrValidation)
	}
	role := normalizeGlobalRole(defaultString(input.Role, "staff"))
	if !isValid(validGlobalRoles, role) {
		return "", "", "", "", fmt.Errorf("%w: invalid role %q", ErrValidation, role)
	}
	email, err := normalizeEmail(input.Email)
	if err != nil {
		return "", "", "", "", err
	}
	displayName := defaultString(input.DisplayName, username)
	return username, displayName, email, role, nil
}

func unusablePasswordHash() (string, error) {
	token, err := security.RandomToken()
	if err != nil {
		return "", err
	}
	return security.HashPassword(token)
}

func insertUserTx(tx *sql.Tx, username, displayName, email, role, hash string, passwordResetRequired bool) (User, error) {
	now := time.Now().UTC()
	result, err := tx.Exec(
		`INSERT INTO users (username, display_name, email, role, password_hash, password_reset_required, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		username, displayName, nullEmptyString(email), role, hash, boolInt(passwordResetRequired), formatTime(now), formatTime(now),
	)
	if err != nil {
		return User{}, normalizeSQLError(err)
	}
	id, _ := result.LastInsertId()
	return User{
		ID:                    id,
		Username:              username,
		DisplayName:           displayName,
		Email:                 email,
		Role:                  role,
		PasswordHash:          hash,
		PasswordResetRequired: passwordResetRequired,
		CreatedAt:             now,
		UpdatedAt:             now,
	}, nil
}

func createAccountLinkTx(tx *sql.Tx, userID int64, purpose string, expiresFor time.Duration) (AccountLink, string, error) {
	purpose = strings.TrimSpace(purpose)
	if !isValid(validAccountLinkPurposes, purpose) {
		return AccountLink{}, "", fmt.Errorf("%w: invalid account link purpose %q", ErrValidation, purpose)
	}
	if expiresFor <= 0 {
		expiresFor = 24 * time.Hour
	}
	token, err := security.RandomToken()
	if err != nil {
		return AccountLink{}, "", err
	}
	now := time.Now().UTC()
	if _, err := tx.Exec(
		`UPDATE account_links SET used_at = ? WHERE user_id = ? AND used_at IS NULL`,
		formatTime(now), userID,
	); err != nil {
		return AccountLink{}, "", err
	}
	result, err := tx.Exec(
		`INSERT INTO account_links (user_id, purpose, token_hash, expires_at, created_at) VALUES (?, ?, ?, ?, ?)`,
		userID, purpose, security.HashToken(token), formatTime(now.Add(expiresFor)), formatTime(now),
	)
	if err != nil {
		return AccountLink{}, "", normalizeSQLError(err)
	}
	id, _ := result.LastInsertId()
	return AccountLink{
		ID:        id,
		UserID:    userID,
		Purpose:   purpose,
		ExpiresAt: now.Add(expiresFor),
		CreatedAt: now,
	}, token, nil
}

func (s *Store) accountLinkByToken(token string) (AccountLink, User, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return AccountLink{}, User{}, err
	}
	defer tx.Rollback()
	link, user, err := accountLinkByTokenTx(tx, token)
	if err != nil {
		return AccountLink{}, User{}, err
	}
	if err := tx.Commit(); err != nil {
		return AccountLink{}, User{}, err
	}
	return link, user, nil
}

func accountLinkByTokenTx(tx *sql.Tx, token string) (AccountLink, User, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return AccountLink{}, User{}, ErrNotFound
	}
	row := tx.QueryRow(`
		SELECT al.id, al.user_id, al.purpose, al.expires_at, al.used_at, al.created_at,
		       u.id, u.username, u.display_name, u.email, u.role, u.password_hash, u.disabled, u.password_reset_required, u.created_at, u.updated_at
		FROM account_links al
		JOIN users u ON u.id = al.user_id
		WHERE al.token_hash = ?`, security.HashToken(token))

	var link AccountLink
	var user User
	var expires, created, userCreated, userUpdated string
	var used sql.NullString
	var email sql.NullString
	var disabled int
	var resetRequired int
	if err := row.Scan(
		&link.ID, &link.UserID, &link.Purpose, &expires, &used, &created,
		&user.ID, &user.Username, &user.DisplayName, &email, &user.Role, &user.PasswordHash,
		&disabled, &resetRequired, &userCreated, &userUpdated,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AccountLink{}, User{}, ErrNotFound
		}
		return AccountLink{}, User{}, err
	}
	link.ExpiresAt = parseTime(expires)
	link.UsedAt = parseNullTime(used)
	link.CreatedAt = parseTime(created)
	user.Email = nullString(email)
	user.Role = normalizeGlobalRole(user.Role)
	user.Disabled = disabled != 0
	user.PasswordResetRequired = resetRequired != 0
	user.CreatedAt = parseTime(userCreated)
	user.UpdatedAt = parseTime(userUpdated)
	if link.UsedAt != nil || !link.ExpiresAt.After(time.Now().UTC()) || user.Disabled {
		return AccountLink{}, User{}, ErrNotFound
	}
	return link, user, nil
}

func getUserTx(tx *sql.Tx, id int64) (User, error) {
	row := tx.QueryRow(`SELECT id, username, display_name, email, role, password_hash, disabled, password_reset_required, created_at, updated_at FROM users WHERE id = ?`, id)
	var user User
	var disabled int
	var resetRequired int
	var email sql.NullString
	var created, updated string
	if err := row.Scan(&user.ID, &user.Username, &user.DisplayName, &email, &user.Role, &user.PasswordHash, &disabled, &resetRequired, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, err
	}
	user.Email = nullString(email)
	user.Role = normalizeGlobalRole(user.Role)
	user.Disabled = disabled != 0
	user.PasswordResetRequired = resetRequired != 0
	user.CreatedAt = parseTime(created)
	user.UpdatedAt = parseTime(updated)
	return user, nil
}

func scanUser(rows scanner) (User, error) {
	var user User
	var disabled int
	var resetRequired int
	var email sql.NullString
	var created, updated string
	if err := rows.Scan(&user.ID, &user.Username, &user.DisplayName, &email, &user.Role, &disabled, &resetRequired, &created, &updated); err != nil {
		return User{}, err
	}
	user.Email = nullString(email)
	user.Role = normalizeGlobalRole(user.Role)
	user.Disabled = disabled != 0
	user.PasswordResetRequired = resetRequired != 0
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
	row := s.db.QueryRow(`SELECT id, username, display_name, email, role, password_hash, disabled, password_reset_required, created_at, updated_at FROM users WHERE username = ?`, username)
	var user User
	var disabled int
	var resetRequired int
	var email sql.NullString
	var created, updated string
	if err := row.Scan(&user.ID, &user.Username, &user.DisplayName, &email, &user.Role, &user.PasswordHash, &disabled, &resetRequired, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, err
	}
	user.Email = nullString(email)
	user.Role = normalizeGlobalRole(user.Role)
	user.Disabled = disabled != 0
	user.PasswordResetRequired = resetRequired != 0
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
