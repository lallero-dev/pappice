package store

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestStoreCreateUpdateCommentAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tracker.json")
	tracker, err := Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	issue, err := tracker.CreateIssue(CreateIssue{
		Title:    "Crash on import",
		Project:  "Core",
		Severity: "crash",
		Priority: "urgent",
		Tags:     []string{"import", "Import", "regression"},
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
	issues = reloaded.ListIssues(Filter{Project: "Core"})
	if len(issues) != 1 {
		t.Fatalf("project issues = %d, want 1", len(issues))
	}
}

func TestStoreValidation(t *testing.T) {
	tracker, err := Open(filepath.Join(t.TempDir(), "tracker.json"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	_, err = tracker.CreateIssue(CreateIssue{Severity: "minor", Priority: "normal"})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("empty title error = %v, want ErrValidation", err)
	}

	issue, err := tracker.CreateIssue(CreateIssue{Title: "Bad status"})
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

	session, _, err := tracker.CreateSession(admin.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if user, ok := tracker.UserBySession(session); !ok || user.ID != admin.ID {
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
	hooks := tracker.ListWebhooksForEvent("issue.created")
	if len(hooks) != 1 || hooks[0].ID != hook.ID {
		t.Fatalf("event hooks = %#v", hooks)
	}
}
