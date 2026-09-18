package store

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pappice/internal/security"
)

func TestUserUpdatesAndLastAdmin(t *testing.T) {
	tracker := openTestStore(t, filepath.Join(t.TempDir(), "tracker.db"))
	admin, err := tracker.CreateFirstAdmin(CreateUser{Password: "correct horse", Email: "admin@example.test"})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if _, err := tracker.UpdateUser(admin.ID, UpdateUser{Disabled: new(true)}); !errors.Is(err, ErrValidation) {
		t.Fatalf("disable sole admin error = %v, want ErrValidation", err)
	}
	user, err := tracker.CreateUser(CreateUser{Password: "correct horse", Email: "bob@example.test"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	updatedUser, err := tracker.UpdateUser(user.ID, UpdateUser{
		DisplayName: new("Customer Bob"),
		Email:       new("customer-bob@example.test"),
		Role:        new("customer"),
		Password:    new("new password"),
	})
	if err != nil {
		t.Fatalf("update user: %v", err)
	}
	if updatedUser.Role != "customer" || updatedUser.Email != "customer-bob@example.test" {
		t.Fatalf("updated user = %#v", updatedUser)
	}
	users := mustListUsers(t, tracker)
	if len(users) != 2 {
		t.Fatalf("users = %#v", users)
	}

	if err := tracker.DeleteUser(user.ID, EventContext{}); err != nil {
		t.Fatal(err)
	}
}

func TestStoreSurfacesAuthenticationQueryErrors(t *testing.T) {
	tracker := openTestStore(t, filepath.Join(t.TempDir(), "tracker.db"))
	admin, err := tracker.CreateFirstAdmin(CreateUser{Email: "admin@example.test", Password: "correct horse"})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	session, _, _, err := tracker.CreateSession(admin.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	_, token, err := tracker.CreateAPIToken(admin.ID, CreateAPIToken{Name: "test"})
	if err != nil {
		t.Fatalf("create API token: %v", err)
	}
	if err := tracker.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	if _, err := tracker.SetupRequired(); err == nil {
		t.Fatal("setup lookup on closed store returned no error")
	}
	if _, _, err := tracker.UserBySession(session); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("session lookup error = %v, want database error", err)
	}
	if _, err := tracker.UserByAPIToken(token); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("API token lookup error = %v, want database error", err)
	}
}

func TestAccountLinksAndPasswordResetLifecycle(t *testing.T) {
	tracker := openTestStore(t, filepath.Join(t.TempDir(), "tracker.db"))
	if _, err := tracker.CreateFirstAdmin(CreateUser{Email: "admin@example.test", Password: "correct horse"}); err != nil {
		t.Fatalf("create first admin: %v", err)
	}

	user, link, setupToken, err := tracker.CreateUserWithSetupLink(CreateUser{
		Email: "pending@example.test",
		Role:  "staff",
	}, time.Hour)
	if err != nil {
		t.Fatalf("create pending user: %v", err)
	}
	if !user.PasswordResetRequired || link.Purpose != "setup" || setupToken == "" {
		t.Fatalf("pending account = %#v link=%#v token=%q", user, link, setupToken)
	}
	if _, err := tracker.Authenticate("pending@example.test", "correct horse"); !errors.Is(err, ErrPasswordResetRequired) {
		t.Fatalf("pending authenticate error = %v, want ErrPasswordResetRequired", err)
	}
	if _, _, _, err := tracker.CreateSession(user.ID); !errors.Is(err, ErrPasswordResetRequired) {
		t.Fatalf("pending create session error = %v, want ErrPasswordResetRequired", err)
	}

	foundLink, foundUser, err := tracker.GetAccountLink(setupToken)
	if err != nil {
		t.Fatalf("get setup link: %v", err)
	}
	if foundLink.ID != link.ID || foundUser.ID != user.ID || foundUser.PasswordHash != "" {
		t.Fatalf("setup link lookup = %#v user=%#v", foundLink, foundUser)
	}
	activated, err := tracker.ConsumeAccountLink(setupToken, "correct horse", EventContext{})
	if err != nil {
		t.Fatalf("consume setup link: %v", err)
	}
	if activated.PasswordResetRequired {
		t.Fatalf("activated user still requires password reset: %#v", activated)
	}
	if _, _, err := tracker.GetAccountLink(setupToken); !errors.Is(err, ErrNotFound) {
		t.Fatalf("used setup link error = %v, want ErrNotFound", err)
	}
	if _, err := tracker.Authenticate("pending@example.test", "correct horse"); err != nil {
		t.Fatalf("authenticate activated user: %v", err)
	}
	session, _, _, err := tracker.CreateSession(user.ID)
	if err != nil {
		t.Fatalf("create activated session: %v", err)
	}
	extraSession, _, _, err := tracker.CreateSession(user.ID)
	if err != nil {
		t.Fatalf("create extra activated session: %v", err)
	}
	if _, err := tracker.ChangePassword(user.ID, "wrong password", "changed password", session, EventContext{}); !errors.Is(err, ErrValidation) {
		t.Fatalf("wrong current password error = %v, want ErrValidation", err)
	}
	changedUser, err := tracker.ChangePassword(user.ID, "correct horse", "changed password", session, EventContext{})
	if err != nil {
		t.Fatalf("change password: %v", err)
	}
	if changedUser.PasswordResetRequired {
		t.Fatalf("changed user requires reset: %#v", changedUser)
	}
	if _, err := tracker.Authenticate("pending@example.test", "changed password"); err != nil {
		t.Fatalf("authenticate changed password: %v", err)
	}
	if _, _, err := tracker.UserBySession(session); err != nil {
		t.Fatalf("kept session error = %v", err)
	}
	if _, _, err := tracker.UserBySession(extraSession); !errors.Is(err, ErrNotFound) {
		t.Fatalf("removed session error = %v, want ErrNotFound", err)
	}
	resetUser, resetLink, resetToken, err := tracker.CreatePasswordResetLink(user.ID, time.Hour, EventContext{})
	if err != nil {
		t.Fatalf("create reset link: %v", err)
	}
	if !resetUser.PasswordResetRequired || resetLink.Purpose != "reset" || resetToken == "" {
		t.Fatalf("reset link = %#v user=%#v token=%q", resetLink, resetUser, resetToken)
	}
	if _, _, err := tracker.UserBySession(session); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reset-invalidated session error = %v, want ErrNotFound", err)
	}
	if _, err := tracker.Authenticate("pending@example.test", "changed password"); !errors.Is(err, ErrPasswordResetRequired) {
		t.Fatalf("reset-required authenticate error = %v, want ErrPasswordResetRequired", err)
	}
	if _, newerLink, newerToken, err := tracker.CreatePasswordResetLink(user.ID, time.Hour, EventContext{}); err != nil {
		t.Fatalf("create newer reset link: %v", err)
	} else {
		if newerLink.ID == resetLink.ID || newerToken == resetToken {
			t.Fatalf("new reset link did not rotate: %#v %q", newerLink, newerToken)
		}
		if _, _, err := tracker.GetAccountLink(resetToken); !errors.Is(err, ErrNotFound) {
			t.Fatalf("old reset link error = %v, want ErrNotFound", err)
		}
		resetToken = newerToken
	}

	resetComplete, err := tracker.ConsumeAccountLink(resetToken, "better password", EventContext{})
	if err != nil {
		t.Fatalf("consume reset link: %v", err)
	}
	if resetComplete.PasswordResetRequired {
		t.Fatalf("reset user still requires password reset: %#v", resetComplete)
	}
	if _, err := tracker.Authenticate("pending@example.test", "better password"); err != nil {
		t.Fatalf("authenticate reset user: %v", err)
	}

	_, _, expiringToken, err := tracker.CreatePasswordResetLink(user.ID, time.Nanosecond, EventContext{})
	if err != nil {
		t.Fatalf("create expiring reset link: %v", err)
	}
	if _, err := tracker.db.Exec(`UPDATE account_links SET expires_at = ? WHERE token_hash = ?`, formatTime(time.Now().UTC().Add(-time.Hour)), security.HashToken(expiringToken)); err != nil {
		t.Fatalf("expire reset link: %v", err)
	}
	if _, _, err := tracker.GetAccountLink(expiringToken); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired reset link error = %v, want ErrNotFound", err)
	}
}

func TestDeleteUserPreservesTicketHistory(t *testing.T) {
	tracker := openTestStore(t, filepath.Join(t.TempDir(), "tracker.db"))
	admin, err := tracker.CreateFirstAdmin(CreateUser{Email: "admin@example.test", Password: "correct horse"})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	productID := mustListProducts(t, tracker, admin)[0].ID
	staff, err := tracker.CreateUser(CreateUser{Email: "staff@example.test", Password: "correct horse", Role: "staff"})
	if err != nil {
		t.Fatalf("create staff: %v", err)
	}
	statusActor, err := tracker.CreateUser(CreateUser{Email: "status-actor@example.test", Password: "correct horse", Role: "staff"})
	if err != nil {
		t.Fatalf("create status actor: %v", err)
	}
	customer, err := tracker.CreateUser(CreateUser{Email: "customer@example.test", Password: "correct horse", Role: "customer"})
	if err != nil {
		t.Fatalf("create customer: %v", err)
	}
	unused, err := tracker.CreateUser(CreateUser{Email: "unused@example.test", Password: "correct horse", Role: "customer"})
	if err != nil {
		t.Fatalf("create unused account: %v", err)
	}
	for _, member := range []UpsertProductMember{
		{UserID: staff.ID, Role: "staff"},
		{UserID: statusActor.ID, Role: "staff"},
		{UserID: customer.ID, Role: "customer"},
	} {
		if _, err := tracker.UpsertProductMember(productID, member); err != nil {
			t.Fatalf("add %s member: %v", member.Role, err)
		}
	}
	ticket, err := tracker.CreateTicketWithAttachments(CreateTicket{
		ProductID:      productID,
		Title:          "Preserved history",
		AssigneeUserID: staff.ID,
		ActorUserID:    customer.ID,
	}, []CreateAttachment{{Filename: "context.txt", ContentType: "text/plain", StorageKey: "context.txt"}})
	if err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	if _, err := tracker.SaveTicket(SaveTicketInput{
		TicketID:    ticket.ID,
		ActorUserID: staff.ID,
		Comment:     &AddComment{Body: "Investigating", Visibility: "public"},
	}); err != nil {
		t.Fatalf("add staff comment: %v", err)
	}
	closed := "closed"
	if _, err := tracker.SaveTicket(SaveTicketInput{
		TicketID:    ticket.ID,
		ActorUserID: statusActor.ID,
		Patch:       UpdateTicket{Status: &closed},
	}); err != nil {
		t.Fatalf("change status: %v", err)
	}

	disabled := true
	if _, err := tracker.UpdateUser(customer.ID, UpdateUser{Disabled: &disabled}); err != nil {
		t.Fatalf("disable customer: %v", err)
	}
	for _, userID := range []int64{customer.ID, staff.ID, statusActor.ID} {
		if err := tracker.DeleteUser(userID, EventContext{}); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "disable it instead") {
			t.Fatalf("delete historical user %d error = %v, want conflict", userID, err)
		}
		if _, err := tracker.GetUser(userID); err != nil {
			t.Fatalf("historical user %d was removed: %v", userID, err)
		}
	}
	if err := tracker.DeleteUser(unused.ID, EventContext{}); err != nil {
		t.Fatalf("delete unused account: %v", err)
	}
	if _, err := tracker.GetUser(unused.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted unused account error = %v, want not found", err)
	}
}
func TestUsersSessionsAndAPITokens(t *testing.T) {
	tracker := openTestStore(t, filepath.Join(t.TempDir(), "tracker.db"))
	if required, err := tracker.SetupRequired(); err != nil || !required {
		t.Fatalf("new store setup required = %v err=%v", required, err)
	}

	admin, err := tracker.CreateFirstAdmin(CreateUser{
		Email:    "admin@example.test",
		Password: "correct horse",
	})
	if err != nil {
		t.Fatalf("create first admin: %v", err)
	}
	if required, err := tracker.SetupRequired(); err != nil || required {
		t.Fatalf("setup required after first admin = %v err=%v", required, err)
	}

	authenticated, err := tracker.Authenticate("admin@example.test", "correct horse")
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if authenticated.PasswordHash != "" {
		t.Fatal("authenticated user leaked password hash")
	}

	legacyUser, err := tracker.CreateUser(CreateUser{Email: "legacy@example.test", Password: "legacy horse"})
	if err != nil {
		t.Fatalf("create legacy hash user: %v", err)
	}
	legacyHash := "pbkdf2-sha256$60000$MDEyMzQ1Njc4OWFiY2RlZg$R5q4Ncg29rBEEUjjeFQAxCjvocVZrvSI1dBok5+gOyk"
	if _, err := tracker.db.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, legacyHash, legacyUser.ID); err != nil {
		t.Fatalf("install legacy hash: %v", err)
	}
	if _, err := tracker.Authenticate("legacy@example.test", "legacy horse"); err != nil {
		t.Fatalf("authenticate legacy hash: %v", err)
	}
	upgraded, err := tracker.GetUser(legacyUser.ID)
	if err != nil {
		t.Fatalf("get upgraded legacy user: %v", err)
	}
	if upgraded.PasswordHash == legacyHash || !strings.Contains(upgraded.PasswordHash, "$120000$") {
		t.Fatalf("legacy hash was not upgraded: %q", upgraded.PasswordHash)
	}

	session, csrf, _, err := tracker.CreateSession(admin.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if csrf == "" {
		t.Fatal("session should include csrf token")
	}
	if user, gotCSRF, err := tracker.UserBySession(session); err != nil || user.ID != admin.ID || gotCSRF != csrf {
		t.Fatalf("session user = %#v err=%v", user, err)
	}
	if err := tracker.DeleteSession(session); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if _, _, err := tracker.UserBySession(session); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted session error = %v, want ErrNotFound", err)
	}
	shortSession, _, expires, err := tracker.CreateSessionFor(admin.ID, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("create short session: %v", err)
	}
	if time.Until(expires) > time.Second {
		t.Fatalf("short session expires too late: %s", expires)
	}
	if _, err := tracker.db.Exec(`UPDATE sessions SET expires_at = ? WHERE token_hash = ?`, formatTime(time.Now().UTC().Add(-time.Hour)), security.HashToken(shortSession)); err != nil {
		t.Fatalf("expire short session: %v", err)
	}
	if _, _, err := tracker.UserBySession(shortSession); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired session error = %v, want ErrNotFound", err)
	}

	token, raw, err := tracker.CreateAPIToken(admin.ID, CreateAPIToken{Name: "cli"})
	if err != nil {
		t.Fatalf("create API token: %v", err)
	}
	if token.Prefix == "" || raw == "" {
		t.Fatalf("token = %#v raw=%q", token, raw)
	}
	if user, err := tracker.UserByAPIToken(raw); err != nil || user.ID != admin.ID {
		t.Fatalf("API token user = %#v err=%v", user, err)
	}
	tokens := mustListAPITokens(t, tracker, admin.ID)
	if len(tokens) != 1 || tokens[0].ID != token.ID || tokens[0].Prefix != token.Prefix {
		t.Fatalf("API tokens = %#v", tokens)
	}
	if err := tracker.DeleteAPIToken(admin.ID, token.ID+100, EventContext{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing API token error = %v, want ErrNotFound", err)
	}
	if err := tracker.DeleteAPIToken(admin.ID, token.ID, EventContext{}); err != nil {
		t.Fatalf("delete API token: %v", err)
	}
	if tokens := mustListAPITokens(t, tracker, admin.ID); len(tokens) != 0 {
		t.Fatalf("API tokens after delete = %#v", tokens)
	}
	if user, err := tracker.UserByAPIToken(raw); !errors.Is(err, ErrNotFound) || user.ID != 0 {
		t.Fatalf("deleted API token user = %#v err=%v", user, err)
	}
}
