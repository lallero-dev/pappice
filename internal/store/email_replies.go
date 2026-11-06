package store

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
)

// A reply credential grants only public replies by the ticket's current requester.
// Recheck membership and account state when issuing and using it.
const emailReplyAccessSQL = `
	FROM tickets t JOIN users u ON u.id = t.requester_user_id
	WHERE t.id = ? AND u.id = ? AND u.email = ? AND u.disabled = 0
	AND (u.role = 'admin' OR EXISTS (
		SELECT 1 FROM product_members pm
		WHERE pm.product_id = t.product_id AND pm.user_id = u.id
		AND pm.role IN ('manager', 'staff', 'customer')
	))`

// EmailReplyToken returns an empty token for recipients who cannot reply.
func (s *Store) EmailReplyToken(ticketID, userID int64, email string) (string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var allowed bool
	if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 `+emailReplyAccessSQL+`)`, ticketID, userID, email).Scan(&allowed); err != nil || !allowed {
		return "", err
	}
	var raw [20]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	_, err = tx.Exec(`
		INSERT INTO email_reply_tokens (ticket_id, user_id, email, token) VALUES (?, ?, ?, ?)
		ON CONFLICT (ticket_id, user_id) DO UPDATE SET email = excluded.email, token = excluded.token
		WHERE email_reply_tokens.email != excluded.email`, ticketID, userID, email, fmt.Sprintf("%x", raw))
	if err != nil {
		return "", err
	}
	var token string
	if err := tx.QueryRow(`SELECT token FROM email_reply_tokens WHERE ticket_id = ? AND user_id = ?`, ticketID, userID).Scan(&token); err != nil {
		return "", err
	}
	return token, tx.Commit()
}

func (s *Store) SaveEmailReply(token, email, key, body string, attachments []CreateAttachment) (SaveTicketResult, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return SaveTicketResult{}, err
	}
	defer tx.Rollback()
	var ticketID, userID int64
	err = tx.QueryRow(`SELECT ticket_id, user_id FROM email_reply_tokens WHERE token = ? AND email = ?`, token, email).Scan(&ticketID, &userID)
	if errors.Is(err, sql.ErrNoRows) {
		return SaveTicketResult{}, ErrNotFound
	}
	if err != nil {
		return SaveTicketResult{}, err
	}
	var allowed bool
	if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 `+emailReplyAccessSQL+`)`, ticketID, userID, email).Scan(&allowed); err != nil {
		return SaveTicketResult{}, err
	}
	if !allowed {
		return SaveTicketResult{}, ErrNotFound
	}
	if key == "" {
		return SaveTicketResult{}, fmt.Errorf("%w: email reply key is required", ErrValidation)
	}
	result, err := saveTicketTx(tx, SaveTicketInput{
		TicketID: ticketID, ActorUserID: userID, IdempotencyKey: "email:" + key,
		Comment: &AddComment{Body: body, Visibility: "public"}, Attachments: attachments,
	})
	if err != nil {
		return SaveTicketResult{}, err
	}
	return result, tx.Commit()
}

// EmailMailboxCursor is independent of IMAP's user-controlled Seen flag.
func (s *Store) EmailMailboxCursor(account string, validity uint32) (uint32, error) {
	var uid uint32
	err := s.db.QueryRow(`SELECT last_uid FROM email_mailboxes WHERE account = ? AND uid_validity = ?`, account, validity).Scan(&uid)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return uid, err
}

func (s *Store) AdvanceEmailMailbox(account string, validity, uid uint32) error {
	_, err := s.db.Exec(`
		INSERT INTO email_mailboxes (account, uid_validity, last_uid) VALUES (?, ?, ?)
		ON CONFLICT (account) DO UPDATE SET
		last_uid = CASE WHEN uid_validity = excluded.uid_validity THEN max(last_uid, excluded.last_uid) ELSE excluded.last_uid END,
		uid_validity = excluded.uid_validity`, account, validity, uid)
	return err
}
