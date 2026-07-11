package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"pappice/internal/security"
)

func (s *Store) SetupRequired() (bool, error) {
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return false, err
	}
	return count == 0, nil
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
	product, err := createProductTx(tx, CreateProduct{Key: "PME", Name: "Inbox"})
	if err != nil {
		return User{}, err
	}
	if _, err := tx.Exec(
		`INSERT INTO product_members (product_id, user_id, role, created_at) VALUES (?, ?, 'manager', ?)`,
		product.ID, user.ID, formatTime(time.Now().UTC()),
	); err != nil {
		return User{}, err
	}
	ctx := input.Event
	if ctx.Enabled {
		ctx.Actor = EventActorFromUser(user)
		if err := insertAppEventTx(tx, time.Now().UTC(), ctx, "setup.completed", "user", user.ID, user.Email, nil, nil); err != nil {
			return User{}, err
		}
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
	if err := insertAppEventTx(tx, time.Now().UTC(), input.Event, "user.created", "user", user.ID, user.Email, map[string]any{
		"role": user.Role,
	}, nil); err != nil {
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
	if err := insertAppEventTx(tx, time.Now().UTC(), input.Event, "user.created", "user", user.ID, user.Email, map[string]any{
		"role":       user.Role,
		"setup_link": true,
	}, accountLinkEventPayload(user, token, link.ExpiresAt)); err != nil {
		return User{}, AccountLink{}, "", err
	}
	if err := tx.Commit(); err != nil {
		return User{}, AccountLink{}, "", err
	}
	return publicUserCopy(user), link, token, nil
}

func (s *Store) ListUsers() ([]User, error) {
	rows, err := s.db.Query(`SELECT id, display_name, email, role, disabled, password_reset_required, created_at, updated_at FROM users ORDER BY email, display_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
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
	before := user
	wasActiveAdmin := user.Role == "admin" && !user.Disabled

	if patch.DisplayName != nil {
		user.DisplayName = defaultString(*patch.DisplayName, user.Email)
	}
	if patch.Email != nil {
		email, err := normalizeRequiredEmail(*patch.Email)
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
		user.DisplayName, user.Email, user.Role, user.PasswordHash, boolInt(user.Disabled),
		boolInt(user.PasswordResetRequired), formatTime(user.UpdatedAt), user.ID,
	); err != nil {
		return User{}, normalizeSQLError(err)
	}
	if patch.Password != nil {
		if _, err := tx.Exec(`DELETE FROM sessions WHERE user_id = ?`, user.ID); err != nil {
			return User{}, err
		}
	}
	if user.Disabled && !before.Disabled {
		if err := cancelPendingUserEmailsTx(tx, user.ID); err != nil {
			return User{}, err
		}
	}
	if wasActiveAdmin && (user.Role != "admin" || user.Disabled) {
		if err := requireActiveAdminTx(tx); err != nil {
			return User{}, err
		}
	}
	if err := insertAppEventTx(tx, time.Now().UTC(), patch.Event, "user.updated", "user", user.ID, user.Email, userPatchEventDetails(before, user, patch), nil); err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	return publicUserCopy(user), nil
}

func (s *Store) DeleteUser(id int64, event EventContext) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	user, err := getUserTx(tx, id)
	if err != nil {
		return err
	}
	if err := cancelPendingUserEmailsTx(tx, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM users WHERE id = ?`, id); err != nil {
		return err
	}
	if user.Role == "admin" && !user.Disabled {
		if err := requireActiveAdminTx(tx); err != nil {
			return err
		}
	}
	if err := insertAppEventTx(tx, time.Now().UTC(), event, "user.deleted", "user", user.ID, user.Email, map[string]any{"role": user.Role}, nil); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Authenticate(email, password string) (User, error) {
	email, err := normalizeRequiredEmail(email)
	if err != nil {
		return User{}, fmt.Errorf("%w: invalid email or password", ErrValidation)
	}
	user, err := s.userByEmail(email, true)
	if err != nil {
		return User{}, fmt.Errorf("%w: invalid email or password", ErrValidation)
	}
	if user.Disabled {
		return User{}, fmt.Errorf("%w: user is disabled", ErrValidation)
	}
	if user.PasswordResetRequired {
		return User{}, ErrPasswordResetRequired
	}
	if !security.VerifyPassword(user.PasswordHash, password) {
		return User{}, fmt.Errorf("%w: invalid email or password", ErrValidation)
	}
	s.rehashPasswordIfNeeded(user, password)
	return publicUserCopy(user), nil
}

func (s *Store) rehashPasswordIfNeeded(user User, password string) {
	if !security.PasswordNeedsRehash(user.PasswordHash) {
		return
	}
	hash, err := security.HashPassword(password)
	if err != nil {
		return
	}
	_, _ = s.db.Exec(`UPDATE users SET password_hash = ? WHERE id = ? AND password_hash = ?`, hash, user.ID, user.PasswordHash)
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

func (s *Store) UserBySession(token string) (User, string, error) {
	hash := security.HashToken(token)
	now := formatTime(time.Now().UTC())
	row := s.db.QueryRow(`
		SELECT u.id, u.display_name, u.email, u.role, u.disabled, u.password_reset_required, u.created_at, u.updated_at, s.csrf_token
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
	if err := row.Scan(&user.ID, &user.DisplayName, &email, &user.Role, &disabled, &resetRequired, &created, &updated, &csrf); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, "", ErrNotFound
		}
		return User{}, "", err
	}
	user.Email = nullString(email)
	user.Role = normalizeGlobalRole(user.Role)
	user.Disabled = disabled != 0
	user.PasswordResetRequired = resetRequired != 0
	user.CreatedAt = parseTime(created)
	user.UpdatedAt = parseTime(updated)
	if user.Disabled || user.PasswordResetRequired {
		return User{}, "", ErrNotFound
	}
	return publicUserCopy(user), csrf, nil
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

func (s *Store) ChangePassword(userID int64, currentPassword, newPassword, keepSessionToken string, event EventContext) (User, error) {
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
	if err := insertAppEventTx(tx, now, event, "password.changed", "user", user.ID, user.Email, nil, nil); err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	return publicUserCopy(user), nil
}

func (s *Store) CreatePasswordResetLink(userID int64, expiresFor time.Duration, event EventContext) (User, AccountLink, string, error) {
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
	if err := insertAppEventTx(tx, now, event, "user.password_reset_requested", "user", user.ID, user.Email, nil, accountLinkEventPayload(user, token, link.ExpiresAt)); err != nil {
		return User{}, AccountLink{}, "", err
	}
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

func (s *Store) ConsumeAccountLink(token, password string, event EventContext) (User, error) {
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
	if event.Enabled {
		if event.Actor.UserID == 0 {
			event.Actor = EventActorFromUser(user)
		}
		if err := insertAppEventTx(tx, now, event, "password.changed", "user", user.ID, user.Email, nil, nil); err != nil {
			return User{}, err
		}
	}
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
	token := "pap_" + raw
	now := time.Now().UTC()

	tx, err := s.db.Begin()
	if err != nil {
		return PublicAPIToken{}, "", err
	}
	defer tx.Rollback()
	user, err := getUserTx(tx, userID)
	if err != nil || user.Disabled {
		return PublicAPIToken{}, "", ErrNotFound
	}
	result, err := tx.Exec(
		`INSERT INTO api_tokens (user_id, name, prefix, token_hash, created_at) VALUES (?, ?, ?, ?, ?)`,
		userID, name, token[:12], security.HashToken(token), formatTime(now),
	)
	if err != nil {
		return PublicAPIToken{}, "", err
	}
	id, err := insertedID(result)
	if err != nil {
		return PublicAPIToken{}, "", err
	}
	apiToken := APIToken{
		ID:        id,
		UserID:    userID,
		Name:      name,
		Prefix:    token[:12],
		TokenHash: security.HashToken(token),
		CreatedAt: now,
	}
	if err := insertAppEventTx(tx, now, input.Event, "api_token.created", "api_token", apiToken.ID, apiToken.Name, nil, nil); err != nil {
		return PublicAPIToken{}, "", err
	}
	if err := tx.Commit(); err != nil {
		return PublicAPIToken{}, "", err
	}
	return publicAPIToken(apiToken), token, nil
}

func (s *Store) ListAPITokens(userID int64) ([]PublicAPIToken, error) {
	rows, err := s.db.Query(
		`SELECT id, user_id, name, prefix, created_at, last_used_at FROM api_tokens WHERE user_id = ? ORDER BY created_at DESC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tokens []PublicAPIToken
	for rows.Next() {
		var token PublicAPIToken
		var created string
		var last sql.NullString
		if err := rows.Scan(&token.ID, &token.UserID, &token.Name, &token.Prefix, &created, &last); err != nil {
			return nil, err
		}
		token.CreatedAt = parseTime(created)
		token.LastUsedAt = parseNullTime(last)
		tokens = append(tokens, token)
	}
	return tokens, rows.Err()
}

func (s *Store) DeleteAPIToken(userID, tokenID int64, event EventContext) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var targetName string
	_ = tx.QueryRow(`SELECT name FROM api_tokens WHERE id = ? AND user_id = ?`, tokenID, userID).Scan(&targetName)
	result, err := tx.Exec(`DELETE FROM api_tokens WHERE id = ? AND user_id = ?`, tokenID, userID)
	if err != nil {
		return err
	}
	if err := requireChangedRow(result); err != nil {
		return err
	}
	if strings.TrimSpace(targetName) == "" {
		targetName = fmt.Sprintf("%d", tokenID)
	}
	if err := insertAppEventTx(tx, time.Now().UTC(), event, "api_token.deleted", "api_token", tokenID, targetName, nil, nil); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) UserByAPIToken(token string) (User, error) {
	hash := security.HashToken(token)
	row := s.db.QueryRow(`
		SELECT u.id, u.display_name, u.email, u.role, u.disabled, u.password_reset_required, u.created_at, u.updated_at, t.id, t.last_used_at
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
	if err := row.Scan(&user.ID, &user.DisplayName, &email, &user.Role, &disabled, &resetRequired, &created, &updated, &tokenID, &last); err != nil {
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
	if user.Disabled {
		return User{}, ErrNotFound
	}
	now := time.Now().UTC()
	lastUsed := parseNullTime(last)
	if lastUsed == nil || now.Sub(*lastUsed) > time.Hour {
		_, _ = s.db.Exec(`UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, formatTime(now), tokenID)
	}
	return publicUserCopy(user), nil
}

func (s *Store) GetUser(id int64) (User, error) {
	return scanStoredUser(s.db.QueryRow(userSelectSQL+` WHERE id = ?`, id))
}

func createUserTx(tx *sql.Tx, input CreateUser) (User, error) {
	displayName, email, role, err := normalizeCreateUserInput(input)
	if err != nil {
		return User{}, err
	}
	hash, err := security.HashPassword(input.Password)
	if err != nil {
		return User{}, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	return insertUserTx(tx, displayName, email, role, hash, false)
}

func createPendingUserTx(tx *sql.Tx, input CreateUser) (User, error) {
	displayName, email, role, err := normalizeCreateUserInput(input)
	if err != nil {
		return User{}, err
	}
	hash, err := unusablePasswordHash()
	if err != nil {
		return User{}, err
	}
	return insertUserTx(tx, displayName, email, role, hash, true)
}

func normalizeCreateUserInput(input CreateUser) (string, string, string, error) {
	email, err := normalizeRequiredEmail(input.Email)
	if err != nil {
		return "", "", "", err
	}
	role := normalizeGlobalRole(defaultString(input.Role, "staff"))
	if !isValid(validGlobalRoles, role) {
		return "", "", "", fmt.Errorf("%w: invalid role %q", ErrValidation, role)
	}
	displayName := defaultString(input.DisplayName, email)
	return displayName, email, role, nil
}

func unusablePasswordHash() (string, error) {
	token, err := security.RandomToken()
	if err != nil {
		return "", err
	}
	return security.HashPassword(token)
}

func insertUserTx(tx *sql.Tx, displayName, email, role, hash string, passwordResetRequired bool) (User, error) {
	now := time.Now().UTC()
	result, err := tx.Exec(
		`INSERT INTO users (display_name, email, role, password_hash, password_reset_required, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		displayName, email, role, hash, boolInt(passwordResetRequired), formatTime(now), formatTime(now),
	)
	if err != nil {
		return User{}, normalizeSQLError(err)
	}
	id, err := insertedID(result)
	if err != nil {
		return User{}, err
	}
	return User{
		ID:                    id,
		DisplayName:           displayName,
		Email:                 email,
		Role:                  role,
		PasswordHash:          hash,
		PasswordResetRequired: passwordResetRequired,
		CreatedAt:             now,
		UpdatedAt:             now,
	}, nil
}

func userPatchEventDetails(before, after User, patch UpdateUser) map[string]any {
	details := make(map[string]any)
	if patch.DisplayName != nil && before.DisplayName != after.DisplayName {
		details["display_name_changed"] = true
	}
	if patch.Email != nil && before.Email != after.Email {
		details["email_changed"] = true
	}
	if patch.Role != nil && before.Role != after.Role {
		details["role_from"] = before.Role
		details["role_to"] = after.Role
	}
	if patch.Disabled != nil && before.Disabled != after.Disabled {
		details["disabled"] = after.Disabled
	}
	if len(details) == 0 {
		return nil
	}
	return details
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
	id, err := insertedID(result)
	if err != nil {
		return AccountLink{}, "", err
	}
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
		       u.id, u.display_name, u.email, u.role, u.password_hash, u.disabled, u.password_reset_required, u.created_at, u.updated_at
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
		&user.ID, &user.DisplayName, &email, &user.Role, &user.PasswordHash,
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
	return scanStoredUser(tx.QueryRow(userSelectSQL+` WHERE id = ?`, id))
}

const userSelectSQL = `
	SELECT id, display_name, email, role, password_hash, disabled, password_reset_required, created_at, updated_at
	FROM users`

func scanStoredUser(row scanner) (User, error) {
	var user User
	var disabled int
	var resetRequired int
	var email sql.NullString
	var created, updated string
	if err := row.Scan(&user.ID, &user.DisplayName, &email, &user.Role, &user.PasswordHash, &disabled, &resetRequired, &created, &updated); err != nil {
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
	if err := rows.Scan(&user.ID, &user.DisplayName, &email, &user.Role, &disabled, &resetRequired, &created, &updated); err != nil {
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

func requireActiveAdminTx(tx *sql.Tx) error {
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM users WHERE role = 'admin' AND disabled = 0`).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("%w: at least one active admin is required", ErrValidation)
	}
	return nil
}

func (s *Store) userByEmail(address string, includeHash bool) (User, error) {
	user, err := scanStoredUser(s.db.QueryRow(userSelectSQL+` WHERE lower(email) = ?`, strings.ToLower(strings.TrimSpace(address))))
	if err != nil {
		return User{}, err
	}
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
