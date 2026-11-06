package notify

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime"
	"strings"
	"unicode/utf8"

	"github.com/emersion/go-message"
	_ "github.com/emersion/go-message/charset"
	"github.com/emersion/go-message/mail"
	"github.com/emersion/go-msgauth/authres"
	"golang.org/x/net/html"

	"pappice/internal/security"
	"pappice/internal/store"
)

const (
	MaxReplyBytes = 25 << 20
	ReplyMarker   = "--- Pappice notification ---"
)

// ErrRejected means the message needs operator review, rather than a retry.
var ErrRejected = errors.New("email rejected")

type EmailAttachment struct {
	Filename string
	Data     []byte
}

type EmailReply struct {
	Token       string
	From        string
	Key         string
	Body        string
	Attachments []EmailAttachment
}

func rejected(reason string) error { return fmt.Errorf("%w: %s", ErrRejected, reason) }

func readReply(raw []byte, address, authservID string) (EmailReply, error) {
	var reply EmailReply
	if len(raw) > MaxReplyBytes {
		return reply, rejected("message exceeds 25 MiB")
	}
	r, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		return reply, rejected("invalid MIME message or unsupported charset")
	}
	defer r.Close()
	h := r.Header
	for fields := h.FieldsByKey("Auto-Submitted"); fields.Next(); {
		value, _, _ := strings.Cut(fields.Value(), ";")
		if !strings.EqualFold(strings.TrimSpace(value), "no") {
			return reply, rejected("automatic message")
		}
	}
	if h.Get("Return-Path") == "<>" || h.Get("List-Id") != "" {
		return reply, rejected("bounce or mailing list message")
	}
	switch strings.ToLower(strings.TrimSpace(h.Get("Precedence"))) {
	case "bulk", "list", "junk":
		return reply, rejected("bulk message")
	}
	if mediaType, _, _ := h.ContentType(); mediaType == "multipart/report" {
		return reply, rejected("delivery report")
	}
	from, err := h.AddressList("From")
	if err != nil || len(from) != 1 || h.FieldsByKey("From").Len() != 1 {
		return reply, rejected("one sender is required")
	}
	reply.From, err = store.NormalizeEmail(from[0].Address)
	if err != nil {
		return reply, rejected("invalid sender")
	}
	_, domain, _ := strings.Cut(reply.From, "@")
	if !authenticatedSender(h, authservID, domain) {
		return reply, rejected("missing or failed trusted DMARC result")
	}
	local, domain, _ := strings.Cut(address, "@")
	for _, field := range []string{"To", "Cc"} {
		for fields := h.FieldsByKey(field); fields.Next(); {
			var addresses mail.Header
			addresses.Set(field, fields.Value())
			list, err := addresses.AddressList(field)
			if err != nil {
				return reply, rejected("invalid recipients")
			}
			for _, recipient := range list {
				user, host, _ := strings.Cut(recipient.Address, "@")
				if !strings.EqualFold(host, domain) || !strings.HasPrefix(user, local+"+") {
					continue
				}
				token := strings.TrimPrefix(user, local+"+")
				if reply.Token != "" && reply.Token != token {
					return reply, rejected("multiple reply addresses")
				}
				reply.Token = token
			}
		}
	}
	if len(reply.Token) != 40 || strings.Trim(reply.Token, "0123456789abcdef") != "" {
		return reply, rejected("missing or invalid reply address")
	}
	if h.FieldsByKey("Message-Id").Len() > 1 {
		return reply, rejected("multiple message IDs")
	}
	reply.Key = security.HashToken(string(raw))
	if h.Get("Message-Id") != "" {
		id, err := h.MessageID()
		if err != nil {
			return reply, rejected("invalid message ID")
		}
		reply.Key = security.HashToken("message-id:" + id)
	}
	var plain, rich string
	remaining := int64(MaxReplyBytes)
	for count := 0; ; count++ {
		part, err := r.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil || count >= 100 {
			return reply, rejected("invalid MIME parts or too many parts")
		}
		data, err := io.ReadAll(io.LimitReader(part.Body, remaining+1))
		if err != nil || int64(len(data)) > remaining {
			return reply, rejected("invalid or oversized MIME part")
		}
		remaining -= int64(len(data))
		var header message.Header
		header.Set("Content-Type", part.Header.Get("Content-Type"))
		header.Set("Content-Disposition", part.Header.Get("Content-Disposition"))
		mediaType, params, err := header.ContentType()
		if err != nil {
			return reply, rejected("invalid content type")
		}
		_, disposition, err := header.ContentDisposition()
		if err != nil && header.Get("Content-Disposition") != "" {
			return reply, rejected("invalid content disposition")
		}
		filename := disposition["filename"]
		if filename == "" {
			filename = params["name"]
		}
		_, attachment := part.Header.(*mail.AttachmentHeader)
		if attachment || filename != "" || !strings.HasPrefix(mediaType, "text/") {
			if filename == "" {
				filename = "attachment"
				if extensions, _ := mime.ExtensionsByType(mediaType); len(extensions) > 0 {
					filename += extensions[0]
				}
			}
			reply.Attachments = append(reply.Attachments, EmailAttachment{filename, data})
			continue
		}
		if !utf8.Valid(data) {
			return reply, rejected("invalid text encoding")
		}
		switch mediaType {
		case "text/plain":
			plain += string(data) + "\n"
		case "text/html":
			rich += htmlReplyText(string(data)) + "\n"
		default:
			return reply, rejected("unsupported text content type")
		}
	}
	reply.Body = trimReply(firstNonEmpty(plain, rich))
	if len(reply.Body) > 1<<20 || (reply.Body == "" && len(reply.Attachments) == 0) {
		return reply, rejected("empty reply or text exceeds 1 MiB")
	}
	return reply, nil
}

// The receiving MTA must remove forged results bearing its own authserv-id.
func authenticatedSender(h mail.Header, trustedID, domain string) bool {
	passed := false
	for fields := h.FieldsByKey("Authentication-Results"); fields.Next(); {
		id, results, err := authres.Parse(fields.Value())
		if err != nil || !strings.EqualFold(id, trustedID) {
			continue
		}
		for _, result := range results {
			if dmarc, ok := result.(*authres.DMARCResult); ok {
				if dmarc.Value != authres.ResultPass || !strings.EqualFold(dmarc.From, domain) {
					return false
				}
				passed = true
			}
		}
	}
	return passed
}

// Strip only our explicit separator; guessing at signatures can lose real text.
func trimReply(body string) string {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	for i, line := range lines {
		if strings.TrimLeft(line, " >\t") == ReplyMarker {
			return strings.TrimSpace(strings.Join(lines[:i], "\n"))
		}
	}
	return strings.TrimSpace(body)
}

func htmlReplyText(body string) string {
	z := html.NewTokenizer(strings.NewReader(body))
	var text strings.Builder
	ignored := ""
	for {
		switch z.Next() {
		case html.ErrorToken:
			return text.String()
		case html.StartTagToken, html.EndTagToken, html.SelfClosingTagToken:
			token := z.Token()
			if token.Data == "script" || token.Data == "style" || token.Data == "head" {
				if token.Type == html.EndTagToken && ignored == token.Data {
					ignored = ""
				} else if token.Type == html.StartTagToken && ignored == "" {
					ignored = token.Data
				}
			}
			switch token.Data {
			case "br", "p", "div", "li", "tr", "blockquote":
				text.WriteByte('\n')
			}
		case html.TextToken:
			if ignored == "" {
				text.Write(z.Text())
			}
		}
	}
}
