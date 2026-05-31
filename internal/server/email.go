package server

import (
	"fmt"
	"html"
	"strings"
	"time"

	"pappice/internal/store"
)

func (s *Server) ticketEmailNotifications(event string, ticket store.Ticket, actor store.User, sendAfter time.Time) []store.CreateEmailNotification {
	if !s.options.EmailNotifications {
		return nil
	}
	sendAfter = normalizeNotificationSendAfter(sendAfter)
	recipients := s.store.TicketEmailRecipients(event, ticket, actor)
	if len(recipients) == 0 {
		return nil
	}
	product, _ := s.store.GetProduct(ticket.ProductID)
	subject, textBody, htmlBody := s.ticketEmailContent(event, product, ticket, actor)
	inputs := make([]store.CreateEmailNotification, 0, len(recipients))
	for _, recipient := range recipients {
		inputs = append(inputs, store.CreateEmailNotification{
			ProductID:      ticket.ProductID,
			TicketID:       ticket.ID,
			UserID:         recipient.UserID,
			RecipientEmail: recipient.Email,
			RecipientName:  defaultString(recipient.DisplayName, recipient.Username),
			Event:          event,
			Subject:        subject,
			BodyText:       textBody,
			BodyHTML:       htmlBody,
			SendAfter:      sendAfter,
			Coalesce:       true,
		})
	}
	return inputs
}

func (s *Server) requesterEmailNotifications(event string, ticket store.Ticket, actorName string, sendAfter time.Time) []store.CreateEmailNotification {
	if !s.options.EmailNotifications || strings.TrimSpace(ticket.RequesterEmail) == "" || strings.TrimSpace(ticket.CustomerToken) == "" {
		return nil
	}
	sendAfter = normalizeNotificationSendAfter(sendAfter)
	subject, textBody, htmlBody := s.requesterEmailContent(event, ticket, actorName)
	return []store.CreateEmailNotification{{
		ProductID:      ticket.ProductID,
		TicketID:       ticket.ID,
		UserID:         0,
		RecipientEmail: ticket.RequesterEmail,
		RecipientName:  defaultString(ticket.RequesterName, ticket.RequesterEmail),
		Event:          event,
		Subject:        subject,
		BodyText:       textBody,
		BodyHTML:       htmlBody,
		SendAfter:      sendAfter,
		Coalesce:       true,
	}}
}

func normalizeNotificationSendAfter(sendAfter time.Time) time.Time {
	if sendAfter.IsZero() {
		return time.Now().UTC()
	}
	return sendAfter.UTC()
}

func (s *Server) accountLinkEmailNotifications(event string, user store.User, token string, expiresAt time.Time) []store.CreateEmailNotification {
	if !s.options.EmailNotifications || strings.TrimSpace(user.Email) == "" || strings.TrimSpace(token) == "" {
		return nil
	}
	subject, textBody, htmlBody := s.accountLinkEmailContent(event, user, token, expiresAt)
	return []store.CreateEmailNotification{{
		UserID:         user.ID,
		RecipientEmail: user.Email,
		RecipientName:  defaultString(user.DisplayName, user.Username),
		Event:          event,
		Subject:        subject,
		BodyText:       textBody,
		BodyHTML:       htmlBody,
		Coalesce:       true,
	}}
}

func (s *Server) accountLinkEmailContent(event string, user store.User, token string, expiresAt time.Time) (string, string, string) {
	action := "Set your Pappice password"
	intro := "An account has been created for you in Pappice."
	if event == "account.reset" {
		action = "Reset your Pappice password"
		intro = "A password reset was requested for your Pappice account."
	}
	link := s.accountLinkURL(accountLinkPurpose(event), token)
	subject := action
	layout := emailLayout{
		Kicker: "Pappice account",
		Title:  subject,
		Intro:  intro,
		Fields: []emailField{
			{Label: "Account", Value: defaultString(user.DisplayName, user.Username)},
			{Label: "Username", Value: user.Username},
			{Label: "Expires", Value: expiresAt.Format("2006-01-02 15:04 MST")},
		},
		ActionLabel: action,
		ActionURL:   link,
		Footer:      "This is a one-time link. If you did not expect this email, contact your Pappice administrator.",
	}
	return subject, renderEmailText(layout), renderEmailHTML(layout)
}

func (s *Server) requesterEmailContent(event string, ticket store.Ticket, actorName string) (string, string, string) {
	subject := fmt.Sprintf("[%s] %s: %s", ticket.Key, requesterEmailSubjectAction(event), ticket.Title)
	link := s.ticketURL(ticket.CustomerToken)

	intro := "We received your ticket."
	if strings.TrimSpace(actorName) != "" && event == "ticket.commented" {
		intro = fmt.Sprintf("%s replied to your ticket.", actorName)
	}
	if event == "ticket.updated" && requesterTerminalStatus(ticket.Status) {
		intro = fmt.Sprintf("Your ticket is now %s.", strings.ToLower(requesterStatusLabel(ticket.Status)))
	}

	fields := []emailField{
		{Label: "Ticket", Value: ticket.Key},
		{Label: "Status", Value: ticket.Status},
	}
	if event != "ticket.created" {
		if requesterTerminalStatus(ticket.Status) {
			fields = append(fields, emailField{Label: "Current status", Value: requesterStatusLabel(ticket.Status)})
		}
	}

	blocks := make([]emailBlock, 0, 1)
	if event != "ticket.created" {
		if comment, ok := latestPublicComment(ticket); ok {
			blocks = append(blocks, emailBlock{Title: "Latest public reply", Meta: "from " + comment.Author, Body: comment.Body})
		}
	}

	layout := emailLayout{
		Kicker:      "Pappice customer support",
		Title:       subject,
		Intro:       intro,
		Fields:      fields,
		Blocks:      blocks,
		ActionLabel: "Open your ticket",
		ActionURL:   link,
		Footer:      "Replies to this email are not read. Please open Pappice to continue the conversation.",
	}
	return subject, renderEmailText(layout), renderEmailHTML(layout)
}

func (s *Server) ticketEmailContent(event string, product store.Product, ticket store.Ticket, actor store.User) (string, string, string) {
	actorName := defaultString(actor.DisplayName, actor.Username)
	action := ticketEventAction(event)
	subject := fmt.Sprintf("[%s] %s: %s", ticket.Key, ticketEmailSubjectAction(event), ticket.Title)
	productLabel := defaultString(product.Name, ticket.ProductName)
	productLabel = defaultString(productLabel, ticket.ProductKey)
	link := strings.TrimRight(s.options.PublicURL, "/")
	if link != "" {
		link += "/"
	}

	fields := []emailField{
		{Label: "Ticket", Value: ticket.Key},
		{Label: "Product", Value: productLabel},
		{Label: "Status", Value: ticket.Status},
		{Label: "Priority", Value: ticket.Priority},
		{Label: "Assignee", Value: ticket.Assignee},
		{Label: "Requester", Value: ticket.Reporter},
	}
	blocks := make([]emailBlock, 0, 2)
	if strings.TrimSpace(ticket.Description) != "" {
		blocks = append(blocks, emailBlock{Title: "Description", Body: ticket.Description})
	}
	if event != "ticket.created" {
		if comment, ok := latestPublicComment(ticket); ok {
			blocks = append(blocks, emailBlock{Title: "Latest public reply", Meta: "from " + comment.Author, Body: comment.Body})
		}
	}

	layout := emailLayout{
		Kicker:      "Pappice staff notification",
		Title:       subject,
		Intro:       fmt.Sprintf("%s %s %s.", actorName, strings.ToLower(action), ticket.Key),
		Fields:      fields,
		Blocks:      blocks,
		ActionLabel: "Open in Pappice",
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
	out.WriteString(`<tr><td style="padding:0 28px 20px;">`)
	for _, block := range blocks {
		body := strings.TrimSpace(block.Body)
		if body == "" {
			continue
		}
		title := defaultString(block.Title, "Details")
		out.WriteString(`<div style="border-top:1px solid #e5ebf1;padding:14px 0;">`)
		fmt.Fprintf(out, `<div style="margin:0 0 8px;color:#64748b;font-size:13px;line-height:1.4;"><span style="font-weight:700;">%s</span>`, html.EscapeString(strings.TrimSpace(title)))
		if strings.TrimSpace(block.Meta) != "" {
			fmt.Fprintf(out, ` <span>%s</span>`, html.EscapeString(strings.TrimSpace(block.Meta)))
		}
		out.WriteString(`</div>`)
		fmt.Fprintf(out, `<div style="margin:0;padding:14px 16px;background:#f8fafc;border:1px solid #dfe7ef;border-radius:6px;color:#1f2933;white-space:pre-wrap;">%s</div>`, emailHTMLLines(body))
		out.WriteString(`</div>`)
	}
	out.WriteString(`</td></tr>`)
}

func emailHTMLLines(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n"))
	return strings.ReplaceAll(html.EscapeString(value), "\n", "<br>")
}

func latestPublicComment(ticket store.Ticket) (store.Comment, bool) {
	for i := len(ticket.Comments) - 1; i >= 0; i-- {
		comment := ticket.Comments[i]
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

func ticketEventAction(event string) string {
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

func ticketEmailSubjectAction(event string) string {
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

func accountLinkPurpose(event string) string {
	if event == "account.reset" {
		return "reset"
	}
	return "setup"
}
