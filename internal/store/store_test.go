package store

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestStoreCreateUpdateCommentAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tracker.json")
	tracker, err := Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	admin, err := tracker.CreateFirstAdmin(CreateUser{
		Username: "Admin",
		Password: "correct horse",
	})
	if err != nil {
		t.Fatalf("create first admin: %v", err)
	}
	products := tracker.ListProducts(admin)
	if len(products) != 1 {
		t.Fatalf("products = %d, want 1", len(products))
	}

	issue, err := tracker.CreateIssue(CreateIssue{
		ProductID: products[0].ID,
		Title:     "Cannot import invoice",
		Priority:  "urgent",
	})
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}
	if issue.ID != 1 {
		t.Fatalf("issue ID = %d, want 1", issue.ID)
	}
	if issue.ProductKey != products[0].Key || issue.ProductName != products[0].Name || issue.Product != products[0].Name {
		t.Fatalf("issue product labels = key %q name %q product %q", issue.ProductKey, issue.ProductName, issue.Product)
	}
	byKey, err := tracker.GetIssueByKey(issue.Key)
	if err != nil {
		t.Fatalf("get issue by key: %v", err)
	}
	if byKey.ID != issue.ID {
		t.Fatalf("issue by key ID = %d, want %d", byKey.ID, issue.ID)
	}
	byLowerKey, err := tracker.GetIssueByKey("pme-1")
	if err != nil {
		t.Fatalf("get issue by lowercase key: %v", err)
	}
	if byLowerKey.ID != issue.ID {
		t.Fatalf("issue by lowercase key ID = %d, want %d", byLowerKey.ID, issue.ID)
	}
	readAt := time.Now().UTC()
	if err := tracker.MarkIssueRead(issue.ID, admin.ID, readAt); err != nil {
		t.Fatalf("mark issue read: %v", err)
	}
	readTimes, err := tracker.IssueReadTimes(admin.ID, []int64{issue.ID})
	if err != nil {
		t.Fatalf("issue read times: %v", err)
	}
	if got := readTimes[issue.ID]; got.IsZero() || got.Sub(readAt).Abs() > time.Second {
		t.Fatalf("read time = %v, want near %v", got, readAt)
	}

	status := "assigned"
	assignee := "alice"
	updated, err := tracker.UpdateIssue(issue.ID, UpdateIssue{
		Status:   &status,
		Assignee: &assignee,
	})
	if err != nil {
		t.Fatalf("update issue: %v", err)
	}
	if updated.Status != "assigned" || updated.Assignee != "alice" {
		t.Fatalf("updated issue = %#v", updated)
	}

	withComment, err := tracker.AddComment(issue.ID, AddComment{
		Author: "bob",
		Body:   "Reproduced on Linux.",
	})
	if err != nil {
		t.Fatalf("add comment: %v", err)
	}
	if got := len(withComment.Comments); got != 1 {
		t.Fatalf("comments = %d, want 1", got)
	}

	reloaded, err := Open(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	issues := reloaded.ListIssues(Filter{Query: "linux"})
	if len(issues) != 0 {
		t.Fatalf("comments should not match issue search, got %d", len(issues))
	}
	issues = reloaded.ListIssues(Filter{ProductID: products[0].ID})
	if len(issues) != 1 {
		t.Fatalf("product issues = %d, want 1", len(issues))
	}
}

func TestStoreValidation(t *testing.T) {
	tracker, err := Open(filepath.Join(t.TempDir(), "tracker.json"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	admin, err := tracker.CreateFirstAdmin(CreateUser{Username: "Admin", Password: "correct horse"})
	if err != nil {
		t.Fatalf("create first admin: %v", err)
	}
	productID := tracker.ListProducts(admin)[0].ID

	_, err = tracker.CreateIssue(CreateIssue{ProductID: productID, Priority: "normal"})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("empty title error = %v, want ErrValidation", err)
	}

	issue, err := tracker.CreateIssue(CreateIssue{ProductID: productID, Title: "Bad status"})
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}
	status := "triaged"
	_, err = tracker.UpdateIssue(issue.ID, UpdateIssue{Status: &status})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("bad status error = %v, want ErrValidation", err)
	}

	wantStatuses := []string{"new", "assigned", "resolved", "rejected"}
	if got := Statuses(); !slices.Equal(got, wantStatuses) {
		t.Fatalf("statuses = %#v, want %#v", got, wantStatuses)
	}

	status = "rejected"
	rejected, err := tracker.UpdateIssue(issue.ID, UpdateIssue{Status: &status})
	if err != nil {
		t.Fatalf("reject issue: %v", err)
	}
	if rejected.Status != "rejected" || rejected.ClosedAt == nil {
		t.Fatalf("rejected issue = %#v, want rejected with closed_at", rejected)
	}
}

func TestMetadataAndPublicViews(t *testing.T) {
	if got, want := Priorities(), []string{"low", "normal", "high", "urgent"}; !slices.Equal(got, want) {
		t.Fatalf("priorities = %#v, want %#v", got, want)
	}
	if got, want := Roles(), []string{"admin", "staff", "customer"}; !slices.Equal(got, want) {
		t.Fatalf("roles = %#v, want %#v", got, want)
	}
	if got, want := ProductRoles(), []string{"owner", "agent", "customer", "viewer"}; !slices.Equal(got, want) {
		t.Fatalf("product roles = %#v, want %#v", got, want)
	}
	if got, want := Events(), []string{"ticket.created", "ticket.updated", "ticket.commented", "ticket.assigned"}; !slices.Equal(got, want) {
		t.Fatalf("events = %#v, want %#v", got, want)
	}

	publicUser := ToPublicUser(User{ID: 7, Username: "bob", DisplayName: "Bob", Email: "bob@example.test", Role: "user", Disabled: true})
	if publicUser.Role != "staff" || publicUser.Username != "bob" || !publicUser.Disabled {
		t.Fatalf("public user = %#v", publicUser)
	}
	productID := int64(3)
	hook := Webhook{ID: 9, ProductID: &productID, Name: "hook", URL: "https://example.test/hook", Secret: "secret", Events: []string{"ticket.created"}}
	publicHook := ToPublicWebhook(hook)
	hook.Events[0] = "ticket.updated"
	if !publicHook.HasSecret || publicHook.Events[0] != "ticket.created" || publicHook.ProductID == nil || *publicHook.ProductID != productID {
		t.Fatalf("public hook = %#v", publicHook)
	}
}

func TestSaveIssueIsTransactional(t *testing.T) {
	tracker, err := Open(filepath.Join(t.TempDir(), "tracker.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	admin, err := tracker.CreateFirstAdmin(CreateUser{Username: "Admin", Password: "correct horse"})
	if err != nil {
		t.Fatalf("create first admin: %v", err)
	}
	productID := tracker.ListProducts(admin)[0].ID
	issue, err := tracker.CreateIssue(CreateIssue{ProductID: productID, Title: "Original", Priority: "normal"})
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}

	title := "Changed"
	status := "assigned"
	_, err = tracker.SaveIssue(SaveIssueInput{
		IssueID: issue.ID,
		Patch:   UpdateIssue{Title: &title, Status: &status},
		Comment: &AddComment{
			Author:     "Admin",
			Body:       "This should not be stored",
			Visibility: "private",
		},
	})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("save issue error = %v, want ErrValidation", err)
	}
	unchanged, err := tracker.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("get issue after failed save: %v", err)
	}
	if unchanged.Title != "Original" || unchanged.Status != "new" || len(unchanged.Comments) != 0 {
		t.Fatalf("failed save was not rolled back: %#v", unchanged)
	}

	comment := AddComment{Author: "Admin", Body: "Now assigned", Visibility: "public"}
	assignee := "alice"
	saved, err := tracker.SaveIssue(SaveIssueInput{
		IssueID: issue.ID,
		Patch:   UpdateIssue{Status: &status, Assignee: &assignee},
		Comment: &comment,
	})
	if err != nil {
		t.Fatalf("save issue: %v", err)
	}
	if !saved.HasPatch || !saved.HasComment || !saved.PublicComment || !saved.AssignmentChanged {
		t.Fatalf("save metadata = %#v", saved)
	}
	if saved.Previous.Status != "new" || saved.Issue.Status != "assigned" || len(saved.Issue.Comments) != 1 {
		t.Fatalf("saved issue = %#v", saved)
	}
}

func TestIssueAttachmentsHydrateWithTicketAndComments(t *testing.T) {
	tracker, err := Open(filepath.Join(t.TempDir(), "tracker.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	admin, err := tracker.CreateFirstAdmin(CreateUser{Username: "Admin", Password: "correct horse"})
	if err != nil {
		t.Fatalf("create first admin: %v", err)
	}
	productID := tracker.ListProducts(admin)[0].ID

	issue, err := tracker.CreateIssueWithAttachments(CreateIssue{
		ProductID: productID,
		Title:     "Attached ticket",
	}, []CreateAttachment{{
		Filename:    "request.txt",
		ContentType: "text/plain",
		SizeBytes:   12,
		SHA256:      "ticket-hash",
		StorageKey:  "ti/ck/ticket-hash",
	}}, admin.ID)
	if err != nil {
		t.Fatalf("create issue with attachment: %v", err)
	}
	if len(issue.Attachments) != 1 || issue.Attachments[0].Filename != "request.txt" || issue.Attachments[0].CreatedByUserID != admin.ID {
		t.Fatalf("ticket attachments = %#v", issue.Attachments)
	}

	saved, err := tracker.SaveIssue(SaveIssueInput{
		IssueID: issue.ID,
		Comment: &AddComment{
			Author:     "Admin",
			Visibility: "internal",
		},
		Attachments: []CreateAttachment{{
			Filename:    "diagnosis.txt",
			ContentType: "text/plain",
			SizeBytes:   10,
			SHA256:      "comment-hash",
			StorageKey:  "co/mm/comment-hash",
		}},
		AttachmentUserID: admin.ID,
	})
	if err != nil {
		t.Fatalf("save file-only comment: %v", err)
	}
	if !saved.HasComment || saved.CommentID == 0 || len(saved.Issue.Comments) != 1 {
		t.Fatalf("save result = %#v", saved)
	}
	comment := saved.Issue.Comments[0]
	if comment.Body != "" || comment.Visibility != "internal" || len(comment.Attachments) != 1 || comment.Attachments[0].Filename != "diagnosis.txt" {
		t.Fatalf("comment attachments = %#v", comment)
	}
	attachment, err := tracker.GetAttachment(comment.Attachments[0].ID)
	if err != nil {
		t.Fatalf("get attachment: %v", err)
	}
	if attachment.IssueID != issue.ID || attachment.CommentID == nil || *attachment.CommentID != saved.CommentID {
		t.Fatalf("attachment = %#v", attachment)
	}
}

func TestUsersSessionsTokensAndWebhooks(t *testing.T) {
	tracker, err := Open(filepath.Join(t.TempDir(), "tracker.json"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if !tracker.SetupRequired() {
		t.Fatal("new store should require setup")
	}

	admin, err := tracker.CreateFirstAdmin(CreateUser{
		Username: "Admin",
		Password: "correct horse",
	})
	if err != nil {
		t.Fatalf("create first admin: %v", err)
	}
	if tracker.SetupRequired() {
		t.Fatal("store should not require setup after first admin")
	}

	authenticated, err := tracker.Authenticate("admin", "correct horse")
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if authenticated.PasswordHash != "" {
		t.Fatal("authenticated user leaked password hash")
	}

	session, csrf, _, err := tracker.CreateSession(admin.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if csrf == "" {
		t.Fatal("session should include csrf token")
	}
	if user, gotCSRF, ok := tracker.UserBySession(session); !ok || user.ID != admin.ID || gotCSRF != csrf {
		t.Fatalf("session user = %#v, %v", user, ok)
	}
	if err := tracker.DeleteSession(session); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if _, _, ok := tracker.UserBySession(session); ok {
		t.Fatal("deleted session should not authenticate")
	}
	shortSession, _, expires, err := tracker.CreateSessionFor(admin.ID, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("create short session: %v", err)
	}
	if time.Until(expires) > time.Second {
		t.Fatalf("short session expires too late: %s", expires)
	}
	time.Sleep(30 * time.Millisecond)
	if _, _, ok := tracker.UserBySession(shortSession); ok {
		t.Fatal("expired short session should not authenticate")
	}

	token, raw, err := tracker.CreateAPIToken(admin.ID, CreateAPIToken{Name: "cli"})
	if err != nil {
		t.Fatalf("create API token: %v", err)
	}
	if token.Prefix == "" || raw == "" {
		t.Fatalf("token = %#v raw=%q", token, raw)
	}
	if user, ok := tracker.UserByAPIToken(raw); !ok || user.ID != admin.ID {
		t.Fatalf("API token user = %#v, %v", user, ok)
	}
	tokens := tracker.ListAPITokens(admin.ID)
	if len(tokens) != 1 || tokens[0].ID != token.ID || tokens[0].Prefix != token.Prefix {
		t.Fatalf("API tokens = %#v", tokens)
	}
	if err := tracker.DeleteAPIToken(admin.ID, token.ID+100); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing API token error = %v, want ErrNotFound", err)
	}
	if err := tracker.DeleteAPIToken(admin.ID, token.ID); err != nil {
		t.Fatalf("delete API token: %v", err)
	}
	if tokens := tracker.ListAPITokens(admin.ID); len(tokens) != 0 {
		t.Fatalf("API tokens after delete = %#v", tokens)
	}
	if user, ok := tracker.UserByAPIToken(raw); ok || user.ID != 0 {
		t.Fatalf("deleted API token user = %#v, %v", user, ok)
	}

	enabled := true
	hook, err := tracker.CreateWebhook(CreateWebhook{
		Name:    "local",
		URL:     "http://127.0.0.1/hook",
		Events:  []string{"ticket.created"},
		Enabled: &enabled,
	})
	if err != nil {
		t.Fatalf("create webhook: %v", err)
	}
	productID := tracker.ListProducts(admin)[0].ID
	hooks := tracker.ListWebhooksForEvent("ticket.created", productID)
	if len(hooks) != 1 || hooks[0].ID != hook.ID {
		t.Fatalf("event hooks = %#v", hooks)
	}

	event, err := tracker.RecordAuditEvent(CreateAuditEvent{
		ActorUserID:   admin.ID,
		ActorUsername: admin.Username,
		Action:        "user.created",
		TargetType:    "user",
		TargetID:      admin.ID,
		TargetName:    admin.Username,
		IP:            "127.0.0.1",
		DetailsJSON:   `{"role":"admin"}`,
	})
	if err != nil {
		t.Fatalf("record audit event: %v", err)
	}
	events := tracker.ListAuditEvents(10)
	if len(events) != 1 || events[0].ID != event.ID || events[0].Action != "user.created" || events[0].DetailsJSON == "" {
		t.Fatalf("audit events = %#v", events)
	}
}

func TestAccountLinksAndPasswordResetLifecycle(t *testing.T) {
	tracker, err := Open(filepath.Join(t.TempDir(), "tracker.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	admin, err := tracker.CreateFirstAdmin(CreateUser{Username: "Admin", Password: "correct horse"})
	if err != nil {
		t.Fatalf("create first admin: %v", err)
	}
	_ = admin

	user, link, setupToken, err := tracker.CreateUserWithSetupLink(CreateUser{
		Username: "Pending",
		Email:    "pending@example.test",
		Role:     "staff",
	}, time.Hour)
	if err != nil {
		t.Fatalf("create pending user: %v", err)
	}
	if !user.PasswordResetRequired || link.Purpose != "setup" || setupToken == "" {
		t.Fatalf("pending account = %#v link=%#v token=%q", user, link, setupToken)
	}
	if _, err := tracker.Authenticate("pending", "correct horse"); !errors.Is(err, ErrPasswordResetRequired) {
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
	activated, err := tracker.ConsumeAccountLink(setupToken, "correct horse")
	if err != nil {
		t.Fatalf("consume setup link: %v", err)
	}
	if activated.PasswordResetRequired {
		t.Fatalf("activated user still requires password reset: %#v", activated)
	}
	if _, _, err := tracker.GetAccountLink(setupToken); !errors.Is(err, ErrNotFound) {
		t.Fatalf("used setup link error = %v, want ErrNotFound", err)
	}
	if _, err := tracker.Authenticate("pending", "correct horse"); err != nil {
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
	if _, err := tracker.ChangePassword(user.ID, "wrong password", "changed password", session); !errors.Is(err, ErrValidation) {
		t.Fatalf("wrong current password error = %v, want ErrValidation", err)
	}
	changedUser, err := tracker.ChangePassword(user.ID, "correct horse", "changed password", session)
	if err != nil {
		t.Fatalf("change password: %v", err)
	}
	if changedUser.PasswordResetRequired {
		t.Fatalf("changed user requires reset: %#v", changedUser)
	}
	if _, err := tracker.Authenticate("pending", "changed password"); err != nil {
		t.Fatalf("authenticate changed password: %v", err)
	}
	if _, _, ok := tracker.UserBySession(session); !ok {
		t.Fatal("change password should keep current session")
	}
	if _, _, ok := tracker.UserBySession(extraSession); ok {
		t.Fatal("change password should remove other sessions")
	}
	extraSession, _, _, err = tracker.CreateSession(user.ID)
	if err != nil {
		t.Fatalf("create session before delete user sessions: %v", err)
	}
	if err := tracker.DeleteUserSessions(user.ID, session); err != nil {
		t.Fatalf("delete user sessions: %v", err)
	}
	if _, _, ok := tracker.UserBySession(session); !ok {
		t.Fatal("delete user sessions should keep requested session")
	}
	if _, _, ok := tracker.UserBySession(extraSession); ok {
		t.Fatal("delete user sessions should remove other sessions")
	}

	resetUser, resetLink, resetToken, err := tracker.CreatePasswordResetLink(user.ID, time.Hour)
	if err != nil {
		t.Fatalf("create reset link: %v", err)
	}
	if !resetUser.PasswordResetRequired || resetLink.Purpose != "reset" || resetToken == "" {
		t.Fatalf("reset link = %#v user=%#v token=%q", resetLink, resetUser, resetToken)
	}
	if _, _, ok := tracker.UserBySession(session); ok {
		t.Fatal("password reset should invalidate existing sessions")
	}
	if _, err := tracker.Authenticate("pending", "changed password"); !errors.Is(err, ErrPasswordResetRequired) {
		t.Fatalf("reset-required authenticate error = %v, want ErrPasswordResetRequired", err)
	}
	if _, newerLink, newerToken, err := tracker.CreatePasswordResetLink(user.ID, time.Hour); err != nil {
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

	resetComplete, err := tracker.ConsumeAccountLink(resetToken, "better password")
	if err != nil {
		t.Fatalf("consume reset link: %v", err)
	}
	if resetComplete.PasswordResetRequired {
		t.Fatalf("reset user still requires password reset: %#v", resetComplete)
	}
	if _, err := tracker.Authenticate("pending", "better password"); err != nil {
		t.Fatalf("authenticate reset user: %v", err)
	}

	_, _, expiringToken, err := tracker.CreatePasswordResetLink(user.ID, time.Nanosecond)
	if err != nil {
		t.Fatalf("create expiring reset link: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	if _, _, err := tracker.GetAccountLink(expiringToken); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired reset link error = %v, want ErrNotFound", err)
	}
}

func TestStoreAdminProductWebhookAndFailureLifecycle(t *testing.T) {
	tracker, err := Open(filepath.Join(t.TempDir(), "tracker.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	admin, err := tracker.CreateFirstAdmin(CreateUser{Username: "Admin", Password: "correct horse", Email: "admin@example.test"})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if _, err := tracker.UpdateUser(admin.ID, UpdateUser{Disabled: boolPtr(true)}); !errors.Is(err, ErrValidation) {
		t.Fatalf("disable sole admin error = %v, want ErrValidation", err)
	}
	user, err := tracker.CreateUser(CreateUser{Username: "Bob", Password: "correct horse", Email: "bob@example.test"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	updatedUser, err := tracker.UpdateUser(user.ID, UpdateUser{
		DisplayName: strPtr("Customer Bob"),
		Email:       strPtr("customer-bob@example.test"),
		Role:        strPtr("customer"),
		Password:    strPtr("new password"),
	})
	if err != nil {
		t.Fatalf("update user: %v", err)
	}
	if updatedUser.Role != "customer" || updatedUser.Email != "customer-bob@example.test" {
		t.Fatalf("updated user = %#v", updatedUser)
	}
	users := tracker.ListUsers()
	if len(users) != 2 {
		t.Fatalf("users = %#v", users)
	}

	product, err := tracker.CreateProduct(CreateProduct{Key: "OPS", Name: "Operations"})
	if err != nil {
		t.Fatalf("create product: %v", err)
	}
	product, err = tracker.UpdateProduct(product.ID, UpdateProduct{Name: strPtr("Operations Desk")})
	if err != nil {
		t.Fatalf("update product: %v", err)
	}
	if product.Name != "Operations Desk" {
		t.Fatalf("product = %#v", product)
	}
	if _, err := tracker.UpsertProductMember(product.ID, UpsertProductMember{UserID: user.ID, Role: "customer"}); err != nil {
		t.Fatalf("upsert member: %v", err)
	}
	if role, ok := tracker.ProductRole(user.ID, product.ID); !ok || role != "customer" {
		t.Fatalf("product role = %q %v", role, ok)
	}
	if err := tracker.DeleteProductMember(product.ID, user.ID); err != nil {
		t.Fatalf("delete member: %v", err)
	}
	if _, ok := tracker.ProductRole(user.ID, product.ID); ok {
		t.Fatal("product role should be removed")
	}

	enabled := false
	hook, err := tracker.CreateWebhook(CreateWebhook{
		ProductID: &product.ID,
		Name:      "product hook",
		URL:       "https://hooks.example.test/incoming",
		Events:    []string{"*"},
		Enabled:   &enabled,
	})
	if err != nil {
		t.Fatalf("create webhook: %v", err)
	}
	hooks := tracker.ListWebhooks(&product.ID)
	if len(hooks) != 1 || hooks[0].ID != hook.ID {
		t.Fatalf("product hooks = %#v", hooks)
	}
	enabled = true
	hook, err = tracker.UpdateWebhook(hook.ID, UpdateWebhook{
		Name:    strPtr("renamed hook"),
		Enabled: &enabled,
	})
	if err != nil {
		t.Fatalf("update webhook: %v", err)
	}
	if hook.Name != "renamed hook" || !hook.Enabled {
		t.Fatalf("updated hook = %#v", hook)
	}
	events := []string{"ticket.updated"}
	secret := "manual-secret"
	hook, err = tracker.UpdateWebhook(hook.ID, UpdateWebhook{
		URL:    strPtr("https://hooks.example.test/renamed"),
		Secret: &secret,
		Events: &events,
	})
	if err != nil {
		t.Fatalf("update webhook details: %v", err)
	}
	if hook.URL != "https://hooks.example.test/renamed" || hook.Secret != secret || !slices.Equal(hook.Events, events) {
		t.Fatalf("updated hook details = %#v", hook)
	}
	rotated, rotatedSecret, err := tracker.RotateWebhookSecret(hook.ID)
	if err != nil {
		t.Fatalf("rotate webhook secret: %v", err)
	}
	if rotatedSecret == "" || rotatedSecret == secret || rotated.Secret != rotatedSecret {
		t.Fatalf("rotated hook = %#v secret=%q", rotated, rotatedSecret)
	}
	if err := tracker.RecordDelivery(WebhookDelivery{WebhookID: hook.ID, ProductID: &product.ID, Event: "ticket.created", StatusCode: 204}); err != nil {
		t.Fatalf("record delivery: %v", err)
	}
	deliveries := tracker.ListDeliveries(10)
	if len(deliveries) != 1 || deliveries[0].StatusCode != 204 {
		t.Fatalf("deliveries = %#v", deliveries)
	}

	issue, err := tracker.CreateIssue(CreateIssue{ProductID: product.ID, Title: "Numbered support ticket"})
	if err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	if gotID, ok := tracker.IssueIDByProductNumber(product.ID, issue.Number); !ok || gotID != issue.ID {
		t.Fatalf("ticket by product number = %d %v", gotID, ok)
	}

	queued, err := tracker.EnqueueEmailNotifications([]CreateEmailNotification{{
		UserID:         user.ID,
		RecipientEmail: updatedUser.Email,
		RecipientName:  updatedUser.DisplayName,
		Event:          "ticket.updated",
		Subject:        "Updated",
		BodyText:       "Body",
	}})
	if err != nil {
		t.Fatalf("enqueue email: %v", err)
	}
	if err := tracker.MarkEmailFailed(queued[0].ID, errors.New("temporary failure"), 1); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	notifications := tracker.ListEmailNotifications(10)
	if len(notifications) != 1 || notifications[0].Status != "failed" || notifications[0].LastError != "temporary failure" {
		t.Fatalf("notifications = %#v", notifications)
	}
	stats := tracker.EmailNotificationStats()
	if stats.Total != 1 || stats.Failed != 1 || stats.LastError != "temporary failure" {
		t.Fatalf("email stats = %#v", stats)
	}
	retried, err := tracker.RetryEmailNotification(queued[0].ID)
	if err != nil {
		t.Fatalf("retry email: %v", err)
	}
	if retried.Status != "pending" || retried.Attempts != 0 || retried.LastError != "" {
		t.Fatalf("retried email = %#v", retried)
	}
	if err := tracker.DeleteWebhook(hook.ID); err != nil {
		t.Fatalf("delete webhook: %v", err)
	}
	if err := tracker.DeleteUser(user.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	if err := tracker.DeleteProduct(product.ID); err != nil {
		t.Fatalf("delete product: %v", err)
	}
}

func TestProductMembershipFiltersIssues(t *testing.T) {
	tracker, err := Open(filepath.Join(t.TempDir(), "tracker.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	admin, err := tracker.CreateFirstAdmin(CreateUser{Username: "Admin", Password: "correct horse"})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	visibleProduct := tracker.ListProducts(admin)[0]
	hiddenProduct, err := tracker.CreateProduct(CreateProduct{Key: "OPS", Name: "Operations"})
	if err != nil {
		t.Fatalf("create product: %v", err)
	}
	user, err := tracker.CreateUser(CreateUser{Username: "bob", Password: "correct horse"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := tracker.UpsertProductMember(visibleProduct.ID, UpsertProductMember{UserID: user.ID, Role: "viewer"}); err != nil {
		t.Fatalf("add member: %v", err)
	}
	if _, err := tracker.CreateIssue(CreateIssue{ProductID: visibleProduct.ID, Title: "Visible"}); err != nil {
		t.Fatalf("create visible issue: %v", err)
	}
	if _, err := tracker.CreateIssue(CreateIssue{ProductID: hiddenProduct.ID, Title: "Hidden"}); err != nil {
		t.Fatalf("create hidden issue: %v", err)
	}

	issues := tracker.ListIssuesForUser(Filter{}, user)
	if len(issues) != 1 || issues[0].Title != "Visible" {
		t.Fatalf("visible issues = %#v", issues)
	}
	products := tracker.ListProducts(user)
	if len(products) != 1 || products[0].ID != visibleProduct.ID {
		t.Fatalf("visible products = %#v", products)
	}
}

func TestEmailRecipientsAndOutbox(t *testing.T) {
	tracker, err := Open(filepath.Join(t.TempDir(), "tracker.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	admin, err := tracker.CreateFirstAdmin(CreateUser{Username: "Admin", Password: "correct horse", Email: "admin@example.test"})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	productID := tracker.ListProducts(admin)[0].ID
	customer, err := tracker.CreateUser(CreateUser{Username: "bob", Password: "correct horse", Email: "bob@example.test", Role: "customer"})
	if err != nil {
		t.Fatalf("create customer: %v", err)
	}
	assignee, err := tracker.CreateUser(CreateUser{Username: "alice", Password: "correct horse", Email: "alice@example.test"})
	if err != nil {
		t.Fatalf("create assignee: %v", err)
	}
	if _, err := tracker.UpsertProductMember(productID, UpsertProductMember{UserID: customer.ID, Role: "customer"}); err != nil {
		t.Fatalf("add customer member: %v", err)
	}
	if _, err := tracker.UpsertProductMember(productID, UpsertProductMember{UserID: assignee.ID, Role: "agent"}); err != nil {
		t.Fatalf("add assignee member: %v", err)
	}
	issue, err := tracker.CreateIssue(CreateIssue{
		ProductID: productID,
		Title:     "Notify operators",
		Reporter:  customer.Username,
		Assignee:  assignee.Username,
	})
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}

	createdRecipients := tracker.IssueEmailRecipients("ticket.created", issue, customer)
	if !hasRecipient(createdRecipients, admin.Email) || hasRecipient(createdRecipients, customer.Email) {
		t.Fatalf("created recipients = %#v, want admin only excluding actor customer", createdRecipients)
	}
	commentRecipients := tracker.IssueEmailRecipients("ticket.commented", issue, customer)
	if !hasRecipient(commentRecipients, assignee.Email) || hasRecipient(commentRecipients, customer.Email) {
		t.Fatalf("comment recipients = %#v, want assignee excluding actor customer", commentRecipients)
	}
	updateRecipients := tracker.IssueEmailRecipients("ticket.updated", issue, admin)
	if !hasRecipient(updateRecipients, assignee.Email) || hasRecipient(updateRecipients, customer.Email) {
		t.Fatalf("update recipients = %#v, want assignee excluding customer reporter", updateRecipients)
	}
	assignedRecipients := tracker.IssueEmailRecipients("ticket.assigned", issue, admin)
	if !hasRecipient(assignedRecipients, assignee.Email) {
		t.Fatalf("assigned recipients = %#v, want assignee", assignedRecipients)
	}

	queued, err := tracker.EnqueueEmailNotifications([]CreateEmailNotification{{
		ProductID:      issue.ProductID,
		IssueID:        issue.ID,
		UserID:         assignee.ID,
		RecipientEmail: assignee.Email,
		RecipientName:  assignee.DisplayName,
		Event:          "ticket.assigned",
		Subject:        "[PME-1] Assigned",
		BodyText:       "assigned",
	}})
	if err != nil {
		t.Fatalf("enqueue email: %v", err)
	}
	if len(queued) != 1 || queued[0].Status != "pending" {
		t.Fatalf("queued = %#v", queued)
	}
	claimed, err := tracker.ClaimEmailNotifications(10, time.Minute)
	if err != nil {
		t.Fatalf("claim email: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ID != queued[0].ID || claimed[0].Status != "sending" {
		t.Fatalf("claimed = %#v", claimed)
	}
	if err := tracker.MarkEmailSent(claimed[0].ID); err != nil {
		t.Fatalf("mark sent: %v", err)
	}
	sent, err := tracker.GetEmailNotification(claimed[0].ID)
	if err != nil {
		t.Fatalf("get sent email: %v", err)
	}
	if sent.Status != "sent" || sent.SentAt == nil {
		t.Fatalf("sent = %#v", sent)
	}

	sendAfter := time.Now().UTC().Add(time.Hour)
	first, err := tracker.EnqueueEmailNotifications([]CreateEmailNotification{{
		ProductID:      issue.ProductID,
		IssueID:        issue.ID,
		UserID:         assignee.ID,
		RecipientEmail: assignee.Email,
		Event:          "ticket.updated",
		Subject:        "[PME-1] Ticket update",
		BodyText:       "first update",
		SendAfter:      sendAfter,
		Coalesce:       true,
	}})
	if err != nil {
		t.Fatalf("enqueue first coalesced email: %v", err)
	}
	second, err := tracker.EnqueueEmailNotifications([]CreateEmailNotification{{
		ProductID:      issue.ProductID,
		IssueID:        issue.ID,
		UserID:         assignee.ID,
		RecipientEmail: assignee.Email,
		Event:          "ticket.commented",
		Subject:        "[PME-1] Ticket update",
		BodyText:       "second update",
		SendAfter:      sendAfter.Add(time.Hour),
		Coalesce:       true,
	}})
	if err != nil {
		t.Fatalf("enqueue second coalesced email: %v", err)
	}
	if first[0].ID != second[0].ID || second[0].Event != "ticket.commented" || second[0].BodyText != "second update" {
		t.Fatalf("coalesced emails = first %#v second %#v", first[0], second[0])
	}
	if second[0].NextAttemptAt.Before(sendAfter.Add(59 * time.Minute)) {
		t.Fatalf("coalesced next attempt = %s, want delayed", second[0].NextAttemptAt)
	}
	claimed, err = tracker.ClaimEmailNotifications(10, time.Minute)
	if err != nil {
		t.Fatalf("claim delayed email: %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("claimed delayed email too early: %#v", claimed)
	}
}

func TestPortalTicketTokenAndPublicComments(t *testing.T) {
	tracker, err := Open(filepath.Join(t.TempDir(), "tracker.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	admin, err := tracker.CreateFirstAdmin(CreateUser{Username: "Admin", Password: "correct horse", Email: "admin@example.test"})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	productID := tracker.ListProducts(admin)[0].ID
	ticket, err := tracker.CreateIssue(CreateIssue{
		ProductID:      productID,
		Title:          "Cannot sign in",
		Description:    "Login fails",
		Source:         "portal",
		RequesterName:  "Customer",
		RequesterEmail: "Customer@Example.Test",
	})
	if err != nil {
		t.Fatalf("create portal ticket: %v", err)
	}
	if ticket.Source != "portal" || ticket.CustomerToken == "" || ticket.RequesterEmail != "customer@example.test" {
		t.Fatalf("ticket fields = %#v", ticket)
	}
	if _, err := tracker.AddComment(ticket.ID, AddComment{Author: "Admin", Body: "Internal diagnosis", Visibility: "internal"}); err != nil {
		t.Fatalf("add internal note: %v", err)
	}
	if _, err := tracker.AddComment(ticket.ID, AddComment{Author: "Admin", Body: "Public reply", Visibility: "public"}); err != nil {
		t.Fatalf("add public reply: %v", err)
	}
	publicTicket, err := tracker.GetIssueByCustomerToken(ticket.CustomerToken)
	if err != nil {
		t.Fatalf("get ticket by token: %v", err)
	}
	if len(publicTicket.Comments) != 1 || publicTicket.Comments[0].Body != "Public reply" {
		t.Fatalf("public comments = %#v", publicTicket.Comments)
	}
	if _, err := tracker.GetIssueByCustomerToken("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing token error = %v, want ErrNotFound", err)
	}
}

func hasRecipient(recipients []EmailRecipient, email string) bool {
	for _, recipient := range recipients {
		if recipient.Email == email {
			return true
		}
	}
	return false
}

func strPtr(value string) *string {
	return &value
}

func boolPtr(value bool) *bool {
	return &value
}
