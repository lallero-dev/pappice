package store

import (
	"errors"
	"path/filepath"
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
	projects := tracker.ListProjects(admin)
	if len(projects) != 1 {
		t.Fatalf("projects = %d, want 1", len(projects))
	}

	issue, err := tracker.CreateIssue(CreateIssue{
		ProjectID: projects[0].ID,
		Title:     "Cannot import invoice",
		Priority:  "urgent",
		Tags:      []string{"import", "Import", "regression"},
	})
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}
	if issue.ID != 1 {
		t.Fatalf("issue ID = %d, want 1", issue.ID)
	}
	if len(issue.Tags) != 2 {
		t.Fatalf("tags = %#v, want deduplicated tags", issue.Tags)
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
	issues = reloaded.ListIssues(Filter{ProjectID: projects[0].ID})
	if len(issues) != 1 {
		t.Fatalf("project issues = %d, want 1", len(issues))
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
	projectID := tracker.ListProjects(admin)[0].ID

	_, err = tracker.CreateIssue(CreateIssue{ProjectID: projectID, Priority: "normal"})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("empty title error = %v, want ErrValidation", err)
	}

	issue, err := tracker.CreateIssue(CreateIssue{ProjectID: projectID, Title: "Bad status"})
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}
	status := "triaged"
	_, err = tracker.UpdateIssue(issue.ID, UpdateIssue{Status: &status})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("bad status error = %v, want ErrValidation", err)
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
	projectID := tracker.ListProjects(admin)[0].ID
	hooks := tracker.ListWebhooksForEvent("ticket.created", projectID)
	if len(hooks) != 1 || hooks[0].ID != hook.ID {
		t.Fatalf("event hooks = %#v", hooks)
	}
}

func TestStoreAdminProjectWebhookAndFailureLifecycle(t *testing.T) {
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
		DisplayName: strPtr("Client Bob"),
		Email:       strPtr("client-bob@example.test"),
		Role:        strPtr("client"),
		Password:    strPtr("new password"),
	})
	if err != nil {
		t.Fatalf("update user: %v", err)
	}
	if updatedUser.Role != "client" || updatedUser.Email != "client-bob@example.test" {
		t.Fatalf("updated user = %#v", updatedUser)
	}
	users := tracker.ListUsers()
	if len(users) != 2 {
		t.Fatalf("users = %#v", users)
	}

	project, err := tracker.CreateProject(CreateProject{Key: "OPS", Name: "Operations"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	project, err = tracker.UpdateProject(project.ID, UpdateProject{Name: strPtr("Operations Desk")})
	if err != nil {
		t.Fatalf("update project: %v", err)
	}
	if project.Name != "Operations Desk" {
		t.Fatalf("project = %#v", project)
	}
	if _, err := tracker.UpsertProjectMember(project.ID, UpsertProjectMember{UserID: user.ID, Role: "customer"}); err != nil {
		t.Fatalf("upsert member: %v", err)
	}
	if role, ok := tracker.ProjectRole(user.ID, project.ID); !ok || role != "customer" {
		t.Fatalf("project role = %q %v", role, ok)
	}
	if err := tracker.DeleteProjectMember(project.ID, user.ID); err != nil {
		t.Fatalf("delete member: %v", err)
	}
	if _, ok := tracker.ProjectRole(user.ID, project.ID); ok {
		t.Fatal("project role should be removed")
	}

	enabled := false
	hook, err := tracker.CreateWebhook(CreateWebhook{
		ProjectID: &project.ID,
		Name:      "project hook",
		URL:       "https://hooks.example.test/incoming",
		Events:    []string{"*"},
		Enabled:   &enabled,
	})
	if err != nil {
		t.Fatalf("create webhook: %v", err)
	}
	hooks := tracker.ListWebhooks(&project.ID)
	if len(hooks) != 1 || hooks[0].ID != hook.ID {
		t.Fatalf("project hooks = %#v", hooks)
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
	if err := tracker.RecordDelivery(WebhookDelivery{WebhookID: hook.ID, ProjectID: &project.ID, Event: "ticket.created", StatusCode: 204}); err != nil {
		t.Fatalf("record delivery: %v", err)
	}
	deliveries := tracker.ListDeliveries(10)
	if len(deliveries) != 1 || deliveries[0].StatusCode != 204 {
		t.Fatalf("deliveries = %#v", deliveries)
	}

	issue, err := tracker.CreateIssue(CreateIssue{ProjectID: project.ID, Title: "Numbered support ticket"})
	if err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	if gotID, ok := tracker.IssueIDByProjectNumber(project.ID, issue.Number); !ok || gotID != issue.ID {
		t.Fatalf("ticket by project number = %d %v", gotID, ok)
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
	if err := tracker.DeleteWebhook(hook.ID); err != nil {
		t.Fatalf("delete webhook: %v", err)
	}
	if err := tracker.DeleteUser(user.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	if err := tracker.DeleteProject(project.ID); err != nil {
		t.Fatalf("delete project: %v", err)
	}
}

func TestProjectMembershipFiltersIssues(t *testing.T) {
	tracker, err := Open(filepath.Join(t.TempDir(), "tracker.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	admin, err := tracker.CreateFirstAdmin(CreateUser{Username: "Admin", Password: "correct horse"})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	visibleProject := tracker.ListProjects(admin)[0]
	hiddenProject, err := tracker.CreateProject(CreateProject{Key: "OPS", Name: "Operations"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	user, err := tracker.CreateUser(CreateUser{Username: "bob", Password: "correct horse"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := tracker.UpsertProjectMember(visibleProject.ID, UpsertProjectMember{UserID: user.ID, Role: "viewer"}); err != nil {
		t.Fatalf("add member: %v", err)
	}
	if _, err := tracker.CreateIssue(CreateIssue{ProjectID: visibleProject.ID, Title: "Visible"}); err != nil {
		t.Fatalf("create visible issue: %v", err)
	}
	if _, err := tracker.CreateIssue(CreateIssue{ProjectID: hiddenProject.ID, Title: "Hidden"}); err != nil {
		t.Fatalf("create hidden issue: %v", err)
	}

	issues := tracker.ListIssuesForUser(Filter{}, user)
	if len(issues) != 1 || issues[0].Title != "Visible" {
		t.Fatalf("visible issues = %#v", issues)
	}
	projects := tracker.ListProjects(user)
	if len(projects) != 1 || projects[0].ID != visibleProject.ID {
		t.Fatalf("visible projects = %#v", projects)
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
	projectID := tracker.ListProjects(admin)[0].ID
	customer, err := tracker.CreateUser(CreateUser{Username: "bob", Password: "correct horse", Email: "bob@example.test"})
	if err != nil {
		t.Fatalf("create customer: %v", err)
	}
	assignee, err := tracker.CreateUser(CreateUser{Username: "alice", Password: "correct horse", Email: "alice@example.test"})
	if err != nil {
		t.Fatalf("create assignee: %v", err)
	}
	if _, err := tracker.UpsertProjectMember(projectID, UpsertProjectMember{UserID: customer.ID, Role: "customer"}); err != nil {
		t.Fatalf("add customer member: %v", err)
	}
	if _, err := tracker.UpsertProjectMember(projectID, UpsertProjectMember{UserID: assignee.ID, Role: "agent"}); err != nil {
		t.Fatalf("add assignee member: %v", err)
	}
	issue, err := tracker.CreateIssue(CreateIssue{
		ProjectID: projectID,
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
	assignedRecipients := tracker.IssueEmailRecipients("ticket.assigned", issue, admin)
	if !hasRecipient(assignedRecipients, assignee.Email) {
		t.Fatalf("assigned recipients = %#v, want assignee", assignedRecipients)
	}

	queued, err := tracker.EnqueueEmailNotifications([]CreateEmailNotification{{
		ProjectID:      issue.ProjectID,
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
	projectID := tracker.ListProjects(admin)[0].ID
	ticket, err := tracker.CreateIssue(CreateIssue{
		ProjectID:      projectID,
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
