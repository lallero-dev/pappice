package notify

import (
	"errors"
	"strings"
	"testing"
)

const testReplyAddress = "reply@example.test"
const testReplyToken = "0123456789012345678901234567890123456789"

func replyMail(body string) string {
	return "From: Requester <requester@example.test>\r\n" +
		"To: reply+" + testReplyToken + "@example.test\r\n" +
		"Authentication-Results: mx.example.test; dmarc=pass header.from=example.test\r\n" +
		"Message-ID: <reply-1@example.test>\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n\r\n" + body
}

func TestReadReply(t *testing.T) {
	for _, test := range []struct {
		name, raw, want string
		attachments     int
	}{
		{"plain", replyMail("Ciao!\r\n\r\n> " + ReplyMarker + "\r\n> Old notification"), "Ciao!", 0},
		{"preserve quotations and signatures", replyMail("Use > to quote\n> an example\n-- \nAlice"), "Use > to quote\n> an example\n-- \nAlice", 0},
		{"quoted printable", strings.Replace(replyMail("Caff=E8"), "charset=utf-8", "charset=iso-8859-1\r\nContent-Transfer-Encoding: quoted-printable", 1), "Caffè", 0},
		{"html only", strings.Replace(replyMail("<html><head><style>bad</style></head><body><div>Hello &amp; thanks</div><script>bad</script><blockquote><div>"+ReplyMarker+"</div>Old notification</blockquote></body></html>"), "text/plain", "text/html", 1), "Hello & thanks", 0},
		{"multipart", strings.Replace(replyMail("--part\r\nContent-Type: text/plain\r\n\r\nHello\r\n--part\r\nContent-Type: text/html\r\n\r\n<b>Hello</b>\r\n--part\r\nContent-Type: text/plain\r\nContent-Disposition: attachment; filename=report.txt\r\nContent-Transfer-Encoding: base64\r\n\r\ncmVwb3J0\r\n--part--\r\n"), "text/plain; charset=utf-8", "multipart/mixed; boundary=part", 1), "Hello", 1},
		{"attachment only", strings.Replace(replyMail("report"), "Content-Type: text/plain; charset=utf-8", "Content-Type: text/plain\r\nContent-Disposition: attachment; filename=report.txt", 1), "", 1},
		{"encoded filename", strings.Replace(replyMail("report"), "Content-Type: text/plain; charset=utf-8", "Content-Type: text/plain\r\nContent-Disposition: attachment; filename=\"=?UTF-8?B?cmVwb3J0LnR4dA==?=\"", 1), "", 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			reply, err := readReply([]byte(test.raw), testReplyAddress, "mx.example.test")
			if err != nil {
				t.Fatal(err)
			}
			if reply.Body != test.want || reply.Token != testReplyToken || reply.From != "requester@example.test" || len(reply.Attachments) != test.attachments {
				t.Fatalf("reply = %#v", reply)
			}
			if test.attachments > 0 && (reply.Attachments[0].Filename != "report.txt" || string(reply.Attachments[0].Data) != "report") {
				t.Fatalf("attachment = %#v", reply.Attachments[0])
			}
		})
	}
}

func TestRejectEmail(t *testing.T) {
	base := replyMail("Hello")
	for _, test := range []struct{ name, raw string }{
		{"missing authentication", strings.Replace(base, "Authentication-Results:", "X-Untrusted:", 1)},
		{"untrusted server", strings.Replace(base, "mx.example.test;", "forged.example.test;", 1)},
		{"failed DMARC", strings.Replace(base, "dmarc=pass", "dmarc=fail", 1)},
		{"wrong authenticated domain", strings.Replace(base, "header.from=example.test", "header.from=other.test", 1)},
		{"injected pass alongside real failure", "Authentication-Results: mx.example.test; dmarc=fail header.from=example.test\r\n" + base},
		{"duplicate sender", "From: attacker@example.test\r\n" + base},
		{"multiple senders", strings.Replace(base, "Requester <requester@example.test>", "requester@example.test, attacker@example.test", 1)},
		{"auto response", "Auto-Submitted: auto-replied\r\n" + base},
		{"own notification", "Auto-Submitted: auto-generated\r\n" + base},
		{"bounce", "Return-Path: <>\r\n" + base},
		{"mailing list", "List-Id: list.example.test\r\n" + base},
		{"bulk", "Precedence: bulk\r\n" + base},
		{"missing token", strings.Replace(base, "+"+testReplyToken, "", 1)},
		{"ambiguous tokens", "Cc: reply+aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa@example.test\r\n" + base},
		{"multiple message IDs", "Message-ID: <second@example.test>\r\n" + base},
		{"empty", replyMail(ReplyMarker + "\nOld message")},
		{"invalid charset", strings.Replace(base, "charset=utf-8", "charset=unknown-encoding", 1)},
		{"invalid utf8", replyMail("\xff")},
		{"malformed attachment header", "Content-Disposition: attachment; filename=\"unterminated\r\n" + base},
		{"malformed multipart", strings.Replace(replyMail("--part\r\nContent-Type: text/plain\r\n\r\nHello"), "text/plain; charset=utf-8", "multipart/mixed; boundary=part", 1)},
		{"oversized body", replyMail(strings.Repeat("x", (1<<20)+1))},
		{"oversized email", strings.Repeat("x", MaxReplyBytes+1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := readReply([]byte(test.raw), testReplyAddress, "mx.example.test"); !errors.Is(err, ErrRejected) {
				t.Fatalf("error = %v, want rejection", err)
			}
		})
	}
}

func TestReplyDeliveryIdentity(t *testing.T) {
	first, err := readReply([]byte(replyMail("Hello")), testReplyAddress, "mx.example.test")
	if err != nil {
		t.Fatal(err)
	}
	second, err := readReply([]byte("Received: another delivery\r\n"+replyMail("Hello")), testReplyAddress, "mx.example.test")
	if err != nil || first.Key != second.Key {
		t.Fatalf("redelivery key changed: %v", err)
	}
	withoutID := strings.Replace(replyMail("Hello"), "Message-ID: <reply-1@example.test>\r\n", "", 1)
	first, err = readReply([]byte(withoutID), testReplyAddress, "mx.example.test")
	if err != nil || first.Key == "" {
		t.Fatalf("missing-ID fallback: %v", err)
	}
	second, err = readReply([]byte(withoutID), testReplyAddress, "mx.example.test")
	if err != nil || first.Key != second.Key {
		t.Fatalf("missing-ID retry changed: %v", err)
	}
}

func FuzzReadReply(f *testing.F) {
	f.Add([]byte(replyMail("Hello")))
	f.Add([]byte("From: invalid\r\n\r\n"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		_, _ = readReply(raw, testReplyAddress, "mx.example.test")
	})
}
