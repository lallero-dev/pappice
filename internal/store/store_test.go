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
		Title:     "Crash on import",
		Severity:  "crash",
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

	_, err = tracker.CreateIssue(CreateIssue{ProjectID: projectID, Severity: "minor", Priority: "normal"})
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
		Events:  []string{"issue.created"},
		Enabled: &enabled,
	})
	if err != nil {
		t.Fatalf("create webhook: %v", err)
	}
	projectID := tracker.ListProjects(admin)[0].ID
	hooks := tracker.ListWebhooksForEvent("issue.created", projectID)
	if len(hooks) != 1 || hooks[0].ID != hook.ID {
		t.Fatalf("event hooks = %#v", hooks)
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
	reporter, err := tracker.CreateUser(CreateUser{Username: "bob", Password: "correct horse", Email: "bob@example.test"})
	if err != nil {
		t.Fatalf("create reporter: %v", err)
	}
	assignee, err := tracker.CreateUser(CreateUser{Username: "alice", Password: "correct horse", Email: "alice@example.test"})
	if err != nil {
		t.Fatalf("create assignee: %v", err)
	}
	if _, err := tracker.UpsertProjectMember(projectID, UpsertProjectMember{UserID: reporter.ID, Role: "reporter"}); err != nil {
		t.Fatalf("add reporter member: %v", err)
	}
	if _, err := tracker.UpsertProjectMember(projectID, UpsertProjectMember{UserID: assignee.ID, Role: "developer"}); err != nil {
		t.Fatalf("add assignee member: %v", err)
	}
	issue, err := tracker.CreateIssue(CreateIssue{
		ProjectID: projectID,
		Title:     "Notify operators",
		Reporter:  reporter.Username,
		Assignee:  assignee.Username,
	})
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}

	createdRecipients := tracker.IssueEmailRecipients("issue.created", issue, reporter)
	if !hasRecipient(createdRecipients, admin.Email) || hasRecipient(createdRecipients, reporter.Email) {
		t.Fatalf("created recipients = %#v, want admin only excluding actor reporter", createdRecipients)
	}
	commentRecipients := tracker.IssueEmailRecipients("issue.commented", issue, reporter)
	if !hasRecipient(commentRecipients, assignee.Email) || hasRecipient(commentRecipients, reporter.Email) {
		t.Fatalf("comment recipients = %#v, want assignee excluding actor reporter", commentRecipients)
	}
	assignedRecipients := tracker.IssueEmailRecipients("issue.assigned", issue, admin)
	if !hasRecipient(assignedRecipients, assignee.Email) {
		t.Fatalf("assigned recipients = %#v, want assignee", assignedRecipients)
	}

	queued, err := tracker.EnqueueEmailNotifications([]CreateEmailNotification{{
		ProjectID:      issue.ProjectID,
		IssueID:        issue.ID,
		UserID:         assignee.ID,
		RecipientEmail: assignee.Email,
		RecipientName:  assignee.DisplayName,
		Event:          "issue.assigned",
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
