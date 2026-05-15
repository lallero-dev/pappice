package server

import (
	"fmt"
	"html"
	"strings"
	"time"

	"pemmece/internal/store"
)

func (s *Server) enqueueIssueEmails(event string, issue store.Issue, actor store.User) {
	if !s.options.EmailNotifications {
		return
	}
	recipients := s.store.IssueEmailRecipients(event, issue, actor)
	if len(recipients) == 0 {
		return
	}
	project, _ := s.store.GetProject(issue.ProjectID)
	subject, textBody, htmlBody := s.issueEmailContent(event, project, issue, actor)
	inputs := make([]store.CreateEmailNotification, 0, len(recipients))
	for _, recipient := range recipients {
		inputs = append(inputs, store.CreateEmailNotification{
			ProjectID:      issue.ProjectID,
			IssueID:        issue.ID,
			UserID:         recipient.UserID,
			RecipientEmail: recipient.Email,
			RecipientName:  defaultString(recipient.DisplayName, recipient.Username),
			Event:          event,
			Subject:        subject,
			BodyText:       textBody,
			BodyHTML:       htmlBody,
			SendAfter:      time.Now().UTC().Add(s.options.EmailBatchDelay),
			Coalesce:       true,
		})
	}
	_, _ = s.store.EnqueueEmailNotifications(inputs)
}

func (s *Server) enqueueRequesterEmail(event string, issue store.Issue, actorName string) {
	if !s.options.EmailNotifications || strings.TrimSpace(issue.RequesterEmail) == "" || strings.TrimSpace(issue.CustomerToken) == "" {
		return
	}
	subject, textBody, htmlBody := s.requesterEmailContent(event, issue, actorName)
	_, _ = s.store.EnqueueEmailNotifications([]store.CreateEmailNotification{{
		ProjectID:      issue.ProjectID,
		IssueID:        issue.ID,
		UserID:         0,
		RecipientEmail: issue.RequesterEmail,
		RecipientName:  defaultString(issue.RequesterName, issue.RequesterEmail),
		Event:          event,
		Subject:        subject,
		BodyText:       textBody,
		BodyHTML:       htmlBody,
		SendAfter:      time.Now().UTC().Add(s.options.EmailBatchDelay),
		Coalesce:       true,
	}})
}

func (s *Server) requesterEmailContent(event string, issue store.Issue, actorName string) (string, string, string) {
	subject := fmt.Sprintf("[%s] %s: %s", issue.Key, requesterEmailSubjectAction(event), issue.Title)
	link := s.ticketURL(issue.CustomerToken)
	var text strings.Builder
	fmt.Fprintf(&text, "%s\n\n", subject)
	if strings.TrimSpace(actorName) != "" && event == "ticket.commented" {
		fmt.Fprintf(&text, "%s replied to your ticket.\n\n", actorName)
	}
	if event != "ticket.created" {
		if comment, ok := latestPublicComment(issue); ok {
			fmt.Fprintf(&text, "Latest public reply from %s:\n%s\n\n", comment.Author, comment.Body)
		}
	}
	if link != "" {
		fmt.Fprintf(&text, "Open your ticket:\n%s\n\n", link)
	}
	text.WriteString("Replies to this email are not read. Please open Pemmece to continue the conversation.\n")

	var htmlBody strings.Builder
	htmlBody.WriteString("<!doctype html><meta charset=\"utf-8\">")
	fmt.Fprintf(&htmlBody, "<h1>%s</h1>", html.EscapeString(subject))
	if strings.TrimSpace(actorName) != "" && event == "ticket.commented" {
		fmt.Fprintf(&htmlBody, "<p><strong>%s</strong> replied to your ticket.</p>", html.EscapeString(actorName))
	}
	if event != "ticket.created" {
		if comment, ok := latestPublicComment(issue); ok {
			fmt.Fprintf(&htmlBody, "<h2>Latest public reply from %s</h2><pre>%s</pre>", html.EscapeString(comment.Author), html.EscapeString(comment.Body))
		}
	}
	if link != "" {
		fmt.Fprintf(&htmlBody, "<p><a href=\"%s\">Open your ticket</a></p>", html.EscapeString(link))
	}
	htmlBody.WriteString("<p>Replies to this email are not read. Please open Pemmece to continue the conversation.</p>")
	return subject, strings.TrimSpace(text.String()), htmlBody.String()
}

func (s *Server) issueEmailContent(event string, project store.Project, issue store.Issue, actor store.User) (string, string, string) {
	actorName := defaultString(actor.DisplayName, actor.Username)
	action := issueEventAction(event)
	subject := fmt.Sprintf("[%s] %s: %s", issue.Key, issueEmailSubjectAction(event), issue.Title)
	projectLabel := issue.ProjectKey
	if project.Name != "" {
		projectLabel = fmt.Sprintf("%s / %s", project.Key, project.Name)
	}
	link := strings.TrimRight(s.options.PublicURL, "/")
	if link != "" {
		link += "/"
	}

	var text strings.Builder
	fmt.Fprintf(&text, "%s %s %s\n\n", actorName, strings.ToLower(action), issue.Key)
	fmt.Fprintf(&text, "Title: %s\n", issue.Title)
	fmt.Fprintf(&text, "Product: %s\n", projectLabel)
	fmt.Fprintf(&text, "Status: %s\nPriority: %s\n", issue.Status, issue.Priority)
	if strings.TrimSpace(issue.Assignee) != "" {
		fmt.Fprintf(&text, "Assignee: %s\n", issue.Assignee)
	}
	if strings.TrimSpace(issue.Reporter) != "" {
		fmt.Fprintf(&text, "Requester: %s\n", issue.Reporter)
	}
	if link != "" {
		fmt.Fprintf(&text, "Open: %s\n", link)
	}
	if strings.TrimSpace(issue.Description) != "" {
		fmt.Fprintf(&text, "\n%s\n", issue.Description)
	}
	if event != "ticket.created" {
		if comment, ok := latestPublicComment(issue); ok {
			fmt.Fprintf(&text, "\nLatest public reply from %s:\n%s\n", comment.Author, comment.Body)
		}
	}

	var htmlBody strings.Builder
	htmlBody.WriteString("<!doctype html><meta charset=\"utf-8\">")
	fmt.Fprintf(&htmlBody, "<p><strong>%s</strong> %s <strong>%s</strong>.</p>", html.EscapeString(actorName), html.EscapeString(strings.ToLower(action)), html.EscapeString(issue.Key))
	htmlBody.WriteString("<dl>")
	fmt.Fprintf(&htmlBody, "<dt>Title</dt><dd>%s</dd>", html.EscapeString(issue.Title))
	fmt.Fprintf(&htmlBody, "<dt>Product</dt><dd>%s</dd>", html.EscapeString(projectLabel))
	fmt.Fprintf(&htmlBody, "<dt>Status</dt><dd>%s</dd>", html.EscapeString(issue.Status))
	fmt.Fprintf(&htmlBody, "<dt>Priority</dt><dd>%s</dd>", html.EscapeString(issue.Priority))
	if strings.TrimSpace(issue.Assignee) != "" {
		fmt.Fprintf(&htmlBody, "<dt>Assignee</dt><dd>%s</dd>", html.EscapeString(issue.Assignee))
	}
	htmlBody.WriteString("</dl>")
	if link != "" {
		fmt.Fprintf(&htmlBody, "<p><a href=\"%s\">Open in Pemmece</a></p>", html.EscapeString(link))
	}
	if strings.TrimSpace(issue.Description) != "" {
		fmt.Fprintf(&htmlBody, "<pre>%s</pre>", html.EscapeString(issue.Description))
	}
	if event != "ticket.created" {
		if comment, ok := latestPublicComment(issue); ok {
			fmt.Fprintf(&htmlBody, "<h2>Latest public reply from %s</h2><pre>%s</pre>", html.EscapeString(comment.Author), html.EscapeString(comment.Body))
		}
	}
	return subject, strings.TrimSpace(text.String()), htmlBody.String()
}

func latestPublicComment(issue store.Issue) (store.Comment, bool) {
	for i := len(issue.Comments) - 1; i >= 0; i-- {
		comment := issue.Comments[i]
		if comment.Visibility == "" || comment.Visibility == "public" {
			return comment, true
		}
	}
	return store.Comment{}, false
}

func issueEventAction(event string) string {
	switch event {
	case "ticket.created":
		return "Created"
	case "ticket.commented":
		return "Commented on"
	case "ticket.assigned":
		return "Assigned"
	default:
		return "Updated"
	}
}

func issueEmailSubjectAction(event string) string {
	if event == "ticket.created" {
		return "New ticket"
	}
	return "Ticket update"
}

func requesterEmailSubjectAction(event string) string {
	if event == "ticket.created" {
		return "Ticket received"
	}
	return "Ticket update"
}
