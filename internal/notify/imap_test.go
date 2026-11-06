package notify

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/backend/memory"
	"github.com/emersion/go-imap/client"
	imapserver "github.com/emersion/go-imap/server"

	"pappice/internal/security"
	"pappice/internal/store"
)

func TestIMAPSplitFetch(t *testing.T) {
	conn, peer := net.Pipe()
	defer conn.Close()
	defer peer.Close()
	if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := fmt.Fprint(peer, "* PREAUTH [CAPABILITY IMAP4rev1] ready\r\n")
		reader := bufio.NewReader(peer)
		for err == nil {
			var line string
			line, err = reader.ReadString('\n')
			if err != nil {
				break
			}
			fields := strings.Fields(line)
			if len(fields) < 2 {
				err = fmt.Errorf("invalid command: %q", line)
				break
			}
			switch strings.ToUpper(fields[1]) {
			case "SELECT":
				_, err = fmt.Fprintf(peer, "* FLAGS (\\Seen)\r\n* 1 EXISTS\r\n* OK [UIDVALIDITY 1] valid\r\n%s OK [READ-WRITE] selected\r\n", fields[0])
			case "UID":
				raw := replyMail("Hello")
				_, err = fmt.Fprintf(peer, "* 1 FETCH (UID 7 FLAGS (\\Seen))\r\n* 1 FETCH (UID 7 FLAGS ())\r\n* 1 FETCH (UID 7 BODY[]<0> {%d}\r\n%s)\r\n%s OK fetched\r\n", len(raw), raw, fields[0])
				done <- err
				return
			default:
				err = fmt.Errorf("unexpected command: %q", line)
			}
		}
		done <- err
	}()
	c, err := client.New(conn)
	if err != nil {
		t.Fatal(err)
	}
	c.Timeout = 3 * time.Second
	c.ErrorLog = log.New(io.Discard, "", 0)
	if _, err := c.Select("INBOX", false); err != nil {
		t.Fatal(err)
	}
	accepted := 0
	w := IMAPWorker{
		Config: IMAPConfig{ReplyAddress: testReplyAddress, AuthservID: "mx.example.test"},
		Accept: func(reply EmailReply) error {
			accepted++
			if reply.Body != "Hello" {
				return fmt.Errorf("wrong body: %q", reply.Body)
			}
			return nil
		},
	}
	if err := w.receive(c, 7); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if accepted != 1 {
		t.Fatalf("accepted %d replies", accepted)
	}
}

func TestIMAPRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	tracker, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tracker.Close() })
	user, err := tracker.CreateFirstAdmin(store.CreateUser{Email: "requester@example.test", Password: "correct horse"})
	if err != nil {
		t.Fatal(err)
	}
	products, err := tracker.ListProducts(user)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := tracker.CreateTicket(store.CreateTicket{ProductID: products[0].ID, Title: "Reply", ActorUserID: user.ID})
	if err != nil {
		t.Fatal(err)
	}
	token, err := tracker.EmailReplyToken(ticket.ID, user.ID, user.Email)
	if err != nil {
		t.Fatal(err)
	}
	be := memory.New()
	mailUser, _ := be.Login(nil, "username", "password")
	mailbox, _ := mailUser.GetMailbox("INBOX")
	box := mailbox.(*memory.Mailbox)
	box.Messages = nil
	for i, raw := range []string{
		replyMail("First"),
		"Auto-Submitted: auto-replied\r\n" + replyMail("Automatic"),
		strings.Replace(replyMail("Second"), "reply-1@", "reply-2@", 1),
	} {
		raw = strings.ReplaceAll(raw, testReplyToken, token)
		flags := []string(nil)
		if i == 0 {
			flags = []string{imap.SeenFlag} // Reading mail elsewhere must not skip it.
		}
		box.Messages = append(box.Messages, &memory.Message{Uid: uint32(i + 1000), Date: time.Now(), Body: []byte(raw), Size: uint32(len(raw)), Flags: flags})
	}
	srv := imapserver.New(be)
	srv.AllowInsecureAuth = true // Loopback test server only.
	srv.ErrorLog = log.New(io.Discard, "", 0)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(listener) }()
	t.Cleanup(func() { _ = srv.Close(); <-done })
	c, err := client.Dial(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	c.Timeout = 3 * time.Second
	defer c.Logout()
	if err := c.Login("username", "password"); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	interrupted := false
	w := IMAPWorker{
		Store: tracker, Logger: log.New(&logs, "", 0),
		Config: IMAPConfig{Address: "mail.example.test:993", Username: "username", Mailbox: "INBOX", ReplyAddress: testReplyAddress, AuthservID: "mx.example.test"},
		Accept: func(reply EmailReply) error {
			_, err := tracker.SaveEmailReply(reply.Token, reply.From, reply.Key, reply.Body, nil)
			if err == nil && reply.Body == "Second" && !interrupted {
				interrupted = true
				return errors.New("interrupted after commit, before acknowledgment")
			}
			return err
		},
	}
	if err := w.processMailbox(context.Background(), c); err == nil {
		t.Fatal("expected interrupted delivery")
	}
	account := security.HashToken(w.Config.Address + "\x00" + w.Config.Username + "\x00" + w.Config.Mailbox)
	if uid, err := tracker.EmailMailboxCursor(account, 1); err != nil || uid != 1001 {
		t.Fatalf("cursor advanced over failed delivery: %d %v", uid, err)
	}
	if err := tracker.Close(); err != nil {
		t.Fatal(err)
	}
	tracker, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	w.Store = tracker
	if err := w.processMailbox(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	// Replaying after a UIDVALIDITY reset must not reopen a closed ticket.
	if _, err := tracker.SaveTicket(store.SaveTicketInput{TicketID: ticket.ID, ActorUserID: user.ID, Patch: store.UpdateTicket{Status: new("closed")}}); err != nil {
		t.Fatal(err)
	}
	if err := tracker.AdvanceEmailMailbox(account, 2, 0); err != nil {
		t.Fatal(err)
	}
	if err := w.processMailbox(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	ticket, err = tracker.GetTicket(ticket.ID)
	if err != nil || len(ticket.Comments) != 2 || ticket.Status != "closed" {
		t.Fatalf("replayed ticket = %#v, %v", ticket, err)
	}
	if !strings.Contains(logs.String(), "automatic message") || strings.Contains(logs.String(), token) {
		t.Fatalf("unexpected logs: %s", logs.String())
	}
	set := new(imap.SeqSet)
	set.AddRange(1000, 1002)
	messages := make(chan *imap.Message, 3)
	if err := c.UidFetch(set, []imap.FetchItem{imap.FetchUid, imap.FetchFlags}, messages); err != nil {
		t.Fatal(err)
	}
	count := 0
	for message := range messages {
		count++
		want := imap.SeenFlag
		if message.Uid == 1001 {
			want = imap.FlaggedFlag
		}
		if !slices.Contains(message.Flags, want) {
			t.Fatalf("UID %d flags = %v", message.Uid, message.Flags)
		}
	}
	if count != 3 {
		t.Fatalf("messages were removed: %d", count)
	}
}

func TestIMAPConfig(t *testing.T) {
	valid := IMAPConfig{Address: "imap.example.test:993", Username: "user", Password: "secret", Mailbox: "INBOX", ReplyAddress: testReplyAddress, AuthservID: "mx.example.test"}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (IMAPConfig{}).Validate(); err != nil {
		t.Fatal(err)
	}
	disabled := valid
	disabled.Address = ""
	if err := disabled.Validate(); err != nil || disabled.Enabled() {
		t.Fatalf("clearing the address did not disable IMAP: %v", err)
	}
	for _, field := range []string{"address", "user", "password", "mailbox", "authserv", "reply", "long address", "display name", "plus"} {
		c := valid
		switch field {
		case "address":
			c.Address = "imap.example.test"
		case "user":
			c.Username = ""
		case "password":
			c.Password = ""
		case "mailbox":
			c.Mailbox = ""
		case "authserv":
			c.AuthservID = ""
		case "reply":
			c.ReplyAddress = ""
		case "long address":
			c.ReplyAddress = strings.Repeat("a", 24) + "@example.test"
		case "display name":
			c.ReplyAddress = "Replies <reply@example.test>"
		case "plus":
			c.ReplyAddress = "reply+other@example.test"
		}
		if err := c.Validate(); err == nil {
			t.Errorf("accepted invalid %s", field)
		}
	}
}

func TestIMAPCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, _ := listener.Accept()
		accepted <- conn
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := IMAPWorker{Config: IMAPConfig{Address: listener.Addr().String()}}
	done := make(chan error, 1)
	go func() { done <- w.ProcessOnce(ctx) }()
	select {
	case conn := <-accepted:
		if conn == nil {
			t.Fatal("connection failed")
		}
		defer conn.Close()
	case <-time.After(3 * time.Second):
		t.Fatal("no connection")
	}
	cancel() // The server never completes the TLS handshake.
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("IMAP did not stop after cancellation")
	}
}
