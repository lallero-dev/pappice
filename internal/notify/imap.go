package notify

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/mail"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"

	"pappice/internal/security"
	"pappice/internal/store"
)

type IMAPConfig struct {
	Address      string
	Username     string
	Password     string
	Mailbox      string
	ReplyAddress string
	AuthservID   string
}

func (c IMAPConfig) Enabled() bool { return c.Address != "" }

func (c IMAPConfig) Validate() error {
	if !c.Enabled() {
		return nil
	}
	host, port, err := net.SplitHostPort(c.Address)
	number, _ := strconv.Atoi(port)
	if err != nil || host == "" || number < 1 || number > 65535 {
		return errors.New("imap address must be host:port (TLS, usually port 993)")
	}
	if c.Username == "" || c.Password == "" || c.Mailbox == "" || strings.TrimSpace(c.AuthservID) == "" {
		return errors.New("imap user, password, mailbox, and authserv-id are required")
	}
	address, err := mail.ParseAddress(c.ReplyAddress)
	if err != nil || address.Address != c.ReplyAddress {
		return errors.New("email reply address must be a bare email address")
	}
	local, _, _ := strings.Cut(c.ReplyAddress, "@")
	if len(local) > 23 || strings.ContainsAny(local, "+\" ") {
		return errors.New("email reply address local part must be at most 23 bytes without plus signs, spaces, or quotes")
	}
	return nil
}

type IMAPWorker struct {
	Config IMAPConfig
	Store  *store.Store
	Accept func(EmailReply) error
	Logger *log.Logger
}

func (w IMAPWorker) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if err := w.ProcessOnce(ctx); err != nil && ctx.Err() == nil {
			w.logf("IMAP poll failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w IMAPWorker) ProcessOnce(ctx context.Context) error {
	dialer := tls.Dialer{NetDialer: &net.Dialer{Timeout: 30 * time.Second}, Config: &tls.Config{MinVersion: tls.VersionTLS12}}
	conn, err := dialer.DialContext(ctx, "tcp", w.Config.Address)
	if err != nil {
		return err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if err := conn.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return err
	}
	c, err := client.New(conn)
	if err != nil {
		return err
	}
	c.Timeout = 30 * time.Second
	c.ErrorLog = log.New(io.Discard, "", 0) // Protocol errors can contain message data.
	if err := c.Login(w.Config.Username, w.Config.Password); err != nil {
		return errors.New("IMAP login failed")
	}
	return w.processMailbox(ctx, c)
}

func (w IMAPWorker) processMailbox(ctx context.Context, c *client.Client) error {
	box, err := c.Select(w.Config.Mailbox, false)
	if err != nil {
		return fmt.Errorf("select mailbox: %w", err)
	}
	if box.UidValidity == 0 || box.UidNext == 0 {
		return errors.New("IMAP server did not report UIDVALIDITY and UIDNEXT")
	}
	account := security.HashToken(w.Config.Address + "\x00" + w.Config.Username + "\x00" + w.Config.Mailbox)
	last, err := w.Store.EmailMailboxCursor(account, box.UidValidity)
	if err != nil || last >= box.UidNext-1 {
		return err
	}
	// Never use Seen as a checkpoint: an operator may read a message first.
	set := new(imap.SeqSet)
	set.AddRange(last+1, box.UidNext-1)
	criteria := imap.NewSearchCriteria()
	criteria.Uid = set
	uids, err := c.UidSearch(criteria)
	if err != nil {
		return err
	}
	slices.Sort(uids)
	end := box.UidNext - 1
	if len(uids) > 100 {
		uids = uids[:100]
		end = uids[len(uids)-1]
	}
	if len(uids) == 0 {
		return w.Store.AdvanceEmailMailbox(account, box.UidValidity, end)
	}
	for _, uid := range uids {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := w.receive(c, uid)
		if err != nil && !errors.Is(err, ErrRejected) {
			return err // A temporary failure must not advance past this message.
		}
		flag := imap.SeenFlag
		if err != nil {
			flag = imap.FlaggedFlag
			w.logf("IMAP UID %d/%d rejected: %v; retained in mailbox for review", box.UidValidity, uid, err)
		}
		set := new(imap.SeqSet)
		set.AddNum(uid)
		if err := c.UidStore(set, imap.FormatFlagsOp(imap.AddFlags, true), []interface{}{flag}, nil); err != nil {
			return err
		}
		if err := w.Store.AdvanceEmailMailbox(account, box.UidValidity, uid); err != nil {
			return err
		}
	}
	// Expunged messages leave gaps; checkpoint those too.
	return w.Store.AdvanceEmailMailbox(account, box.UidValidity, end)
}

func (w IMAPWorker) receive(c *client.Client, uid uint32) error {
	set := new(imap.SeqSet)
	set.AddNum(uid)
	section := &imap.BodySectionName{Peek: true, Partial: []int{0, MaxReplyBytes + 1}}
	// Drain while fetching: a server may send several FETCH responses for a UID.
	messages := make(chan *imap.Message)
	done := make(chan error, 1)
	go func() { done <- c.UidFetch(set, []imap.FetchItem{section.FetchItem()}, messages) }()
	var raw []byte
	var readErr error
	received := false
	for fetched := range messages {
		received = true
		body := fetched.GetBody(section)
		if body != nil && raw == nil && readErr == nil {
			raw, readErr = io.ReadAll(io.LimitReader(body, MaxReplyBytes+1))
		}
	}
	if err := <-done; err != nil {
		return err
	}
	if readErr != nil {
		return readErr
	}
	if !received {
		return nil // Expunged between the search and fetch.
	}
	if raw == nil {
		return errors.New("IMAP message body is missing")
	}
	reply, err := readReply(raw, w.Config.ReplyAddress, w.Config.AuthservID)
	if err != nil {
		return err
	}
	return w.Accept(reply)
}

func (w IMAPWorker) logf(format string, args ...any) {
	if w.Logger != nil {
		w.Logger.Printf(format, args...)
	}
}
