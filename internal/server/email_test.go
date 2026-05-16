package server

import (
	"strings"
	"testing"

	"pemmece/internal/store"
)

func TestRequesterEmailContentUsesReadableLayout(t *testing.T) {
	server := &Server{options: Options{PublicURL: "https://tracker.example.test"}}
	issue := store.Issue{
		Key:           "PME-1",
		ProjectKey:    "PME",
		Title:         "Need <help>",
		Status:        "resolved",
		CustomerToken: "customer-token",
		Comments: []store.Comment{{
			Author:     "Alice",
			Body:       "Please try the updated setup.\nIt should work now.",
			Visibility: "public",
		}},
	}

	subject, textBody, htmlBody := server.requesterEmailContent("ticket.commented", issue, "Alice")

	if subject != "[PME-1] Ticket update: Need <help>" {
		t.Fatalf("subject = %q", subject)
	}
	for _, want := range []string{
		"Alice replied to your ticket.",
		"Ticket: PME-1",
		"Current status: Resolved",
		"Latest public reply from Alice:",
		"Open your ticket:\nhttps://tracker.example.test/",
		"Replies to this email are not read.",
	} {
		if !strings.Contains(textBody, want) {
			t.Fatalf("text body missing %q:\n%s", want, textBody)
		}
	}
	for _, want := range []string{
		"Pemmece customer support",
		"Need &lt;help&gt;",
		"Latest public reply",
		"from Alice",
		`<table role="presentation"`,
		"Please try the updated setup.<br>It should work now.",
	} {
		if !strings.Contains(htmlBody, want) {
			t.Fatalf("html body missing %q:\n%s", want, htmlBody)
		}
	}
	if strings.Contains(htmlBody, "Need <help>") {
		t.Fatalf("html body did not escape title:\n%s", htmlBody)
	}
}

func TestIssueEmailContentUsesReadableLayout(t *testing.T) {
	server := &Server{options: Options{PublicURL: "https://tracker.example.test"}}
	project := store.Project{Key: "PME", Name: "Pemmece"}
	issue := store.Issue{
		Key:         "PME-2",
		ProjectKey:  "PME",
		Title:       "Cannot sign in",
		Description: "Login fails after password reset.",
		Status:      "assigned",
		Priority:    "urgent",
		Assignee:    "dev",
		Reporter:    "customer",
	}
	actor := store.User{Username: "paolo", DisplayName: "Paolo"}

	subject, textBody, htmlBody := server.issueEmailContent("ticket.assigned", project, issue, actor)

	if subject != "[PME-2] Ticket update: Cannot sign in" {
		t.Fatalf("subject = %q", subject)
	}
	for _, want := range []string{
		"Paolo assigned PME-2.",
		"Product: PME / Pemmece",
		"Priority: urgent",
		"Description:\nLogin fails after password reset.",
		"Open in Pemmece:\nhttps://tracker.example.test/",
	} {
		if !strings.Contains(textBody, want) {
			t.Fatalf("text body missing %q:\n%s", want, textBody)
		}
	}
	for _, want := range []string{
		"Pemmece staff notification",
		"PME / Pemmece",
		"Login fails after password reset.",
		`href="https://tracker.example.test/"`,
	} {
		if !strings.Contains(htmlBody, want) {
			t.Fatalf("html body missing %q:\n%s", want, htmlBody)
		}
	}
}
