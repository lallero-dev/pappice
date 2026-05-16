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

	intro := "We received your ticket."
	if strings.TrimSpace(actorName) != "" && event == "ticket.commented" {
		intro = fmt.Sprintf("%s replied to your ticket.", actorName)
	}
	if event == "ticket.updated" && requesterTerminalStatus(issue.Status) {
		intro = fmt.Sprintf("Your ticket is now %s.", strings.ToLower(requesterStatusLabel(issue.Status)))
	}

	fields := []emailField{
		{Label: "Ticket", Value: issue.Key},
		{Label: "Status", Value: issue.Status},
	}
	if event != "ticket.created" {
		if requesterTerminalStatus(issue.Status) {
			fields = append(fields, emailField{Label: "Current status", Value: requesterStatusLabel(issue.Status)})
		}
	}

	blocks := make([]emailBlock, 0, 1)
	if event != "ticket.created" {
		if comment, ok := latestPublicComment(issue); ok {
			blocks = append(blocks, emailBlock{Title: "Latest public reply", Meta: "from " + comment.Author, Body: comment.Body})
		}
	}

	layout := emailLayout{
		Kicker:      "Pemmece customer support",
		Title:       subject,
		Intro:       intro,
		Fields:      fields,
		Blocks:      blocks,
		ActionLabel: "Open your ticket",
		ActionURL:   link,
		Footer:      "Replies to this email are not read. Please open Pemmece to continue the conversation.",
	}
	return subject, renderEmailText(layout), renderEmailHTML(layout)
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

	fields := []emailField{
		{Label: "Ticket", Value: issue.Key},
		{Label: "Product", Value: projectLabel},
		{Label: "Status", Value: issue.Status},
		{Label: "Priority", Value: issue.Priority},
		{Label: "Assignee", Value: issue.Assignee},
		{Label: "Requester", Value: issue.Reporter},
	}
	blocks := make([]emailBlock, 0, 2)
	if strings.TrimSpace(issue.Description) != "" {
		blocks = append(blocks, emailBlock{Title: "Description", Body: issue.Description})
	}
	if event != "ticket.created" {
		if comment, ok := latestPublicComment(issue); ok {
			blocks = append(blocks, emailBlock{Title: "Latest public reply", Meta: "from " + comment.Author, Body: comment.Body})
		}
	}

	layout := emailLayout{
		Kicker:      "Pemmece staff notification",
		Title:       subject,
		Intro:       fmt.Sprintf("%s %s %s.", actorName, strings.ToLower(action), issue.Key),
		Fields:      fields,
		Blocks:      blocks,
		ActionLabel: "Open in Pemmece",
		ActionURL:   link,
	}
	return subject, renderEmailText(layout), renderEmailHTML(layout)
}

type emailLayout struct {
	Kicker      string
	Title       string
	Intro       string
	Fields      []emailField
	Blocks      []emailBlock
	ActionLabel string
	ActionURL   string
	Footer      string
}

type emailField struct {
	Label string
	Value string
}

type emailBlock struct {
	Title string
	Meta  string
	Body  string
}

func renderEmailText(layout emailLayout) string {
	var text strings.Builder
	if strings.TrimSpace(layout.Title) != "" {
		fmt.Fprintf(&text, "%s\n\n", strings.TrimSpace(layout.Title))
	}
	if strings.TrimSpace(layout.Intro) != "" {
		fmt.Fprintf(&text, "%s\n\n", strings.TrimSpace(layout.Intro))
	}
	for _, field := range layout.Fields {
		if strings.TrimSpace(field.Label) == "" || strings.TrimSpace(field.Value) == "" {
			continue
		}
		fmt.Fprintf(&text, "%s: %s\n", strings.TrimSpace(field.Label), strings.TrimSpace(field.Value))
	}
	if len(layout.Fields) > 0 {
		text.WriteString("\n")
	}
	for _, block := range layout.Blocks {
		body := strings.TrimSpace(block.Body)
		if body == "" {
			continue
		}
		if strings.TrimSpace(block.Title) != "" {
			title := strings.TrimSpace(block.Title)
			if strings.TrimSpace(block.Meta) != "" {
				title += " " + strings.TrimSpace(block.Meta)
			}
			fmt.Fprintf(&text, "%s:\n", title)
		}
		fmt.Fprintf(&text, "%s\n\n", body)
	}
	if strings.TrimSpace(layout.ActionURL) != "" {
		label := defaultString(layout.ActionLabel, "Open")
		fmt.Fprintf(&text, "%s:\n%s\n\n", label, strings.TrimSpace(layout.ActionURL))
	}
	if strings.TrimSpace(layout.Footer) != "" {
		fmt.Fprintf(&text, "%s\n", strings.TrimSpace(layout.Footer))
	}
	return strings.TrimSpace(text.String())
}

func renderEmailHTML(layout emailLayout) string {
	var htmlBody strings.Builder
	htmlBody.WriteString(`<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"></head>`)
	htmlBody.WriteString(`<body style="margin:0;background:#f4f6f8;color:#1f2933;font-family:Arial,Helvetica,sans-serif;font-size:15px;line-height:1.5;">`)
	htmlBody.WriteString(`<div style="display:none;max-height:0;overflow:hidden;color:#f4f6f8;">`)
	htmlBody.WriteString(html.EscapeString(strings.TrimSpace(layout.Intro)))
	htmlBody.WriteString(`</div>`)
	htmlBody.WriteString(`<table role="presentation" width="100%" cellspacing="0" cellpadding="0" style="border-collapse:collapse;background:#f4f6f8;"><tr><td align="center" style="padding:24px 12px;">`)
	htmlBody.WriteString(`<table role="presentation" width="100%" cellspacing="0" cellpadding="0" style="border-collapse:collapse;max-width:640px;background:#ffffff;border:1px solid #d8e0e8;border-radius:6px;">`)
	htmlBody.WriteString(`<tr><td style="padding:24px 28px 20px;">`)
	if strings.TrimSpace(layout.Kicker) != "" {
		fmt.Fprintf(&htmlBody, `<div style="font-size:12px;font-weight:700;letter-spacing:0;text-transform:uppercase;color:#52647a;margin-bottom:8px;">%s</div>`, html.EscapeString(strings.TrimSpace(layout.Kicker)))
	}
	fmt.Fprintf(&htmlBody, `<h1 style="margin:0;color:#111827;font-size:22px;line-height:1.25;font-weight:700;">%s</h1>`, html.EscapeString(strings.TrimSpace(layout.Title)))
	if strings.TrimSpace(layout.Intro) != "" {
		fmt.Fprintf(&htmlBody, `<p style="margin:14px 0 0;color:#334155;">%s</p>`, html.EscapeString(strings.TrimSpace(layout.Intro)))
	}
	htmlBody.WriteString(`</td></tr>`)
	writeEmailFieldsHTML(&htmlBody, layout.Fields)
	writeEmailBlocksHTML(&htmlBody, layout.Blocks)
	if strings.TrimSpace(layout.ActionURL) != "" || strings.TrimSpace(layout.Footer) != "" {
		htmlBody.WriteString(`<tr><td style="padding:20px 28px 24px;border-top:1px solid #e5ebf1;">`)
		if strings.TrimSpace(layout.ActionURL) != "" {
			label := defaultString(layout.ActionLabel, "Open")
			fmt.Fprintf(&htmlBody, `<p style="margin:0 0 14px;"><a href="%s" style="color:#1b5f9e;font-weight:700;text-decoration:none;">%s</a></p>`, html.EscapeString(strings.TrimSpace(layout.ActionURL)), html.EscapeString(label))
		}
		if strings.TrimSpace(layout.Footer) != "" {
			fmt.Fprintf(&htmlBody, `<p style="margin:0;color:#64748b;font-size:13px;">%s</p>`, html.EscapeString(strings.TrimSpace(layout.Footer)))
		}
		htmlBody.WriteString(`</td></tr>`)
	}
	htmlBody.WriteString(`</table></td></tr></table></body></html>`)
	return htmlBody.String()
}

func writeEmailFieldsHTML(out *strings.Builder, fields []emailField) {
	hasFields := false
	for _, field := range fields {
		if strings.TrimSpace(field.Label) != "" && strings.TrimSpace(field.Value) != "" {
			hasFields = true
			break
		}
	}
	if !hasFields {
		return
	}
	out.WriteString(`<tr><td style="padding:0 28px 20px;"><table role="presentation" width="100%" cellspacing="0" cellpadding="0" style="border-collapse:collapse;border-top:1px solid #e5ebf1;">`)
	for _, field := range fields {
		label := strings.TrimSpace(field.Label)
		value := strings.TrimSpace(field.Value)
		if label == "" || value == "" {
			continue
		}
		fmt.Fprintf(out, `<tr><td style="padding:9px 12px 9px 0;border-bottom:1px solid #e5ebf1;color:#64748b;font-size:13px;width:34%%;">%s</td><td style="padding:9px 0;border-bottom:1px solid #e5ebf1;color:#111827;font-weight:600;">%s</td></tr>`, html.EscapeString(label), html.EscapeString(value))
	}
	out.WriteString(`</table></td></tr>`)
}

func writeEmailBlocksHTML(out *strings.Builder, blocks []emailBlock) {
	hasBlocks := false
	for _, block := range blocks {
		if strings.TrimSpace(block.Body) != "" {
			hasBlocks = true
			break
		}
	}
	if !hasBlocks {
		return
	}
	out.WriteString(`<tr><td style="padding:0 28px 20px;"><table role="presentation" width="100%" cellspacing="0" cellpadding="0" style="border-collapse:collapse;border-top:1px solid #e5ebf1;">`)
	for _, block := range blocks {
		body := strings.TrimSpace(block.Body)
		if body == "" {
			continue
		}
		title := defaultString(block.Title, "Details")
		fmt.Fprintf(out, `<tr><td valign="top" style="padding:12px 12px 12px 0;border-bottom:1px solid #e5ebf1;color:#64748b;font-size:13px;font-weight:700;width:34%%;">%s</td><td style="padding:12px 0;border-bottom:1px solid #e5ebf1;">`, html.EscapeString(strings.TrimSpace(title)))
		if strings.TrimSpace(block.Meta) != "" {
			fmt.Fprintf(out, `<div style="margin:0 0 8px;color:#64748b;font-size:13px;">%s</div>`, html.EscapeString(strings.TrimSpace(block.Meta)))
		}
		fmt.Fprintf(out, `<div style="margin:0;padding:14px 16px;background:#f8fafc;border:1px solid #dfe7ef;border-radius:6px;color:#1f2933;white-space:pre-wrap;">%s</div>`, emailHTMLLines(body))
		out.WriteString(`</td></tr>`)
	}
	out.WriteString(`</table></td></tr>`)
}

func emailHTMLLines(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n"))
	return strings.ReplaceAll(html.EscapeString(value), "\n", "<br>")
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

func requesterTerminalStatus(status string) bool {
	status = strings.TrimSpace(strings.ToLower(status))
	return status == "resolved" || status == "rejected"
}

func requesterStatusLabel(status string) string {
	switch strings.TrimSpace(strings.ToLower(status)) {
	case "resolved":
		return "Resolved"
	case "rejected":
		return "Rejected"
	default:
		return strings.TrimSpace(status)
	}
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
